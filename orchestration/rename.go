package orchestration

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

// RenameRequest moves a namespace entry or changes its name. Source and
// destination are specified as parent ObjectIDs and entry names.
type RenameRequest struct {
	Context             context.Context
	SourceParentID      shared.ObjectID
	SourceName          string
	DestinationParentID shared.ObjectID
	DestinationName     string
}

type RenameResponse struct{}

// Rename applies POSIX-style namespace rename semantics. Replacement is
// supported only when source and destination have compatible kinds; replacing
// a directory requires it to be empty. Header updates are persisted in order
// and are not crash-atomic across multiple objects.
func (layer *Orchestration) Rename(request RenameRequest) (response RenameResponse, err error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if err = validateNewObjectName(request.SourceName); err != nil {
		return response, err
	}
	if err = validateNewObjectName(request.DestinationName); err != nil {
		return response, err
	}

	sourceParent, err := layer.renameDirectory(request.SourceParentID)
	if err != nil {
		return response, err
	}
	destinationParent, err := layer.renameDirectory(request.DestinationParentID)
	if err != nil {
		return response, err
	}
	source, exists := sourceParent.children[request.SourceName]
	if !exists || source.header.Deleted {
		return response, os.ErrNotExist
	}
	if request.SourceParentID == request.DestinationParentID &&
		request.SourceName == request.DestinationName {
		return response, nil
	}
	if _, protected := layer.protectedObjects[source.header.ObjectID]; protected {
		return response, os.ErrNotExist
	}
	if source.header.Kind == objectKindDirectory && source == layer.root {
		return response, fmt.Errorf("cannot rename root directory: %w", os.ErrInvalid)
	}

	// A directory cannot be moved beneath itself or any descendant.
	if source.header.Kind == objectKindDirectory &&
		layer.renameDirectoryContains(source, destinationParent) {
		return response, syscall.EINVAL
	}

	destination, destinationExists := destinationParent.children[request.DestinationName]
	if destinationExists && (destination == source ||
		(source.file != nil && source.file == destination.file)) {
		// POSIX permits names for the same inode to be renamed onto one
		// another without removing either namespace entry.
		return response, nil
	}
	if destinationExists {
		if err = layer.validateRenameReplacement(source, destination); err != nil {
			return response, err
		}
	}

	if err = ctx.Err(); err != nil {
		return response, err
	}

	// Regular-file renames use the namespace-link lifecycle: publish a
	// HardLink at the destination first, then unlink the source name. This
	// ensures both names refer to the same data object before the source is
	// detached. The sequence is deliberately not crash-atomic; if source
	// unlink fails, both names remain available.
	if source.header.Kind == objectKindFile || source.header.Kind == objectKindHardLink {
		if destinationExists {
			if err = layer.removeRenameDestination(ctx, destinationParent, destination); err != nil {
				return response, fmt.Errorf("remove rename destination: %w", err)
			}
		}
		if _, err = layer.Link(LinkRequest{
			Context: ctx, SourceID: source.header.ObjectID,
			ParentID: request.DestinationParentID, Name: request.DestinationName,
		}); err != nil {
			return response, fmt.Errorf("create destination HardLink: %w", err)
		}
		if _, err = layer.Unlink(UnlinkRequest{
			Context: ctx, ParentID: request.SourceParentID, Name: request.SourceName,
		}); err != nil {
			return response, fmt.Errorf("unlink rename source after destination link: %w", err)
		}
		return response, nil
	}

	// Directly move namespace-only objects (directories and symlinks).
	// Their metadata does not have a shareable file data object.
	if destinationExists {
		if err = layer.removeRenameDestination(ctx, destinationParent, destination); err != nil {
			return response, fmt.Errorf("remove rename destination: %w", err)
		}
	}

	next := source.header
	// File data and mutable attributes are authoritative in runtimeFile.header.
	// Write and SetAttr update that Header without changing the namespace node's
	// copy, so start a rename from the backing Header to avoid restoring stale
	// size, timestamps, or permissions while changing the namespace fields.
	if source.file != nil && source.header.Kind == objectKindFile {
		source.file.mu.RLock()
		next = source.file.header
		source.file.mu.RUnlock()
	}
	parentID := request.DestinationParentID
	next.ParentID = &parentID
	next.Name = request.DestinationName
	next.Ctime = time.Now()
	if err = layer.persistObjectHeader(ctx, next); err != nil {
		return response, fmt.Errorf("persist renamed object: %w", err)
	}

	delete(sourceParent.children, request.SourceName)
	source.header = next
	layer.headers[next.ObjectID] = next
	if source.file != nil && next.Kind == objectKindFile {
		source.file.mu.Lock()
		source.file.header = next
		source.file.mu.Unlock()
	}
	if destinationParent.children == nil {
		destinationParent.children = make(map[string]*runtimeObject)
	}
	destinationParent.children[request.DestinationName] = source
	return response, nil
}

func (layer *Orchestration) renameDirectory(objectID shared.ObjectID) (*runtimeObject, error) {
	object, exists := layer.objects[objectID]
	if !exists || object.header.Deleted {
		return nil, os.ErrNotExist
	}
	if object.header.Kind != objectKindDirectory {
		return nil, fmt.Errorf("parent %s: %w", objectID.String(), errNotDirectory)
	}
	if _, protected := layer.protectedObjects[objectID]; protected {
		return nil, os.ErrNotExist
	}
	return object, nil
}

func (layer *Orchestration) renameDirectoryContains(root, candidate *runtimeObject) bool {
	for current := candidate; current != nil; {
		if current == root {
			return true
		}
		if current.header.ParentID == nil {
			return false
		}
		current = layer.objects[*current.header.ParentID]
	}
	return false
}

func (layer *Orchestration) validateRenameReplacement(source, destination *runtimeObject) error {
	sourceIsDirectory := source.header.Kind == objectKindDirectory
	destinationIsDirectory := destination.header.Kind == objectKindDirectory
	if sourceIsDirectory != destinationIsDirectory {
		if sourceIsDirectory {
			return syscall.ENOTDIR
		}
		return syscall.EISDIR
	}
	if destinationIsDirectory && len(destination.children) != 0 {
		return syscall.ENOTEMPTY
	}
	return nil
}

// removeRenameDestination unpublishes and logically removes the destination
// entry using the same kind-specific lifecycle rules as Unlink.
func (layer *Orchestration) removeRenameDestination(ctx context.Context, parent, object *runtimeObject) error {
	switch object.header.Kind {
	case objectKindFile:
		return layer.unlinkFile(ctx, parent, object)
	case objectKindSymlink:
		return layer.unlinkSymlink(ctx, parent, object)
	case objectKindHardLink:
		return layer.unlinkHardLink(ctx, parent, object)
	case objectKindDirectory:
		next := object.header
		next.Deleted = true
		next.Ctime = time.Now()
		if err := layer.persistObjectHeader(ctx, next); err != nil {
			return err
		}
		object.header = next
		layer.headers[next.ObjectID] = next
		delete(parent.children, next.Name)
		layer.enqueueDeletion(next.ObjectID)
		return nil
	default:
		return fmt.Errorf("unsupported destination object kind: %w", os.ErrInvalid)
	}
}
