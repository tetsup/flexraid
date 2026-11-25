package distribution

import (
	"context"
	"errors"
	"os"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type CleanupRequest struct {
	Context          context.Context
	OldVersions      bool
	InvalidStripes   bool
	IncompleteChunks bool
	ExcludeObjectIDs []shared.ObjectID
}
type CleanupResponse struct{ DeletedFiles int }

type cleanupFile struct {
	candidate cleanupCandidateKey
	location  chunkLocation
}

func (d *Distribution) Cleanup(request CleanupRequest) (response CleanupResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() { logOperation(d, "Cleanup", started, err, "deleted_files", response.DeletedFiles) }()
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if !request.IncompleteChunks && !request.InvalidStripes && !request.OldVersions {
		return response, nil
	}

	excluded := make(map[shared.ObjectID]struct{}, len(request.ExcludeObjectIDs))
	for _, id := range request.ExcludeObjectIDs {
		excluded[id] = struct{}{}
	}

	// Select immutable FileKeys under mapMu, then release it before physical I/O.
	d.mapMu.Lock()
	items := make([]cleanupFile, 0)
	seen := make(map[struct {
		ioIndex int
		key     storage.FileKey
	}]struct{})
	appendItem := func(location chunkLocation) {
		identity := struct {
			ioIndex int
			key     storage.FileKey
		}{ioIndex: location.ioIndex, key: location.key}
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		items = append(items, cleanupFile{
			candidate: cleanupCandidateKey{
				ioIndex: location.ioIndex, objectID: location.key.ObjectID,
				stripeID: location.key.StripeID, method: cleanupDeleteOldVersion,
			},
			location: location,
		})
	}
	for objectID, locations := range d.orphans {
		if _, skip := excluded[objectID]; skip {
			continue
		}
		for _, location := range locations {
			current := d.objects[objectID][location.key.StripeID]
			remove := request.InvalidStripes || request.IncompleteChunks ||
				(request.OldVersions && (current == nil || location.key.StripeVersion < current.version))
			if remove {
				appendItem(location)
			}
		}
	}
	for key, versions := range d.versions {
		if _, skip := excluded[key.objectID]; skip {
			continue
		}
		current := d.objects[key.objectID][key.stripeID]
		for versionNo, version := range versions {
			removeVersion := (request.OldVersions && !currentVersion(current, versionNo)) ||
				(request.InvalidStripes && !version.recoverable)
			if !removeVersion && !request.IncompleteChunks {
				continue
			}
			if !removeVersion && version.recoverable {
				continue
			}
			for _, location := range version.chunks {
				appendItem(location)
			}
		}
	}
	d.mapMu.Unlock()

	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		_, deleteErr := d.ios[item.location.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: item.location.key})
		d.recordChunkOperation("Delete", deleteErr)
		if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
			return response, deleteErr
		}

		// Reconcile only the exact FileKey that was deleted. A concurrent
		// foreground write may have published a newer immutable version.
		key := stripeIDKey{objectID: item.location.key.ObjectID, stripeID: item.location.key.StripeID}
		d.mapMu.Lock()
		d.removeCleanupLocationLocked(item.candidate, item.location)
		d.reselectStripeLocked(key)
		d.refreshStripeCleanupQueueLocked(key)
		d.mapMu.Unlock()
		response.DeletedFiles++
	}
	return response, nil
}

func (d *Distribution) reselectStripeLocked(key stripeIDKey) {
	stripes := d.objects[key.objectID]
	if stripes == nil {
		return
	}
	current := stripes[key.stripeID]
	versions := d.versions[key]
	if current != nil && versions[current.version] != nil {
		return
	}
	var latest storage.StripeVersion
	var selected *versionInfo
	for versionNo, candidate := range versions {
		if candidate.recoverable && (selected == nil || versionNo > latest) {
			latest, selected = versionNo, candidate
		}
	}
	if selected == nil {
		delete(stripes, key.stripeID)
		if len(stripes) == 0 {
			delete(d.objects, key.objectID)
		}
		return
	}
	stripes[key.stripeID] = &stripeInfo{
		version: latest, method: selected.method, chunkSize: selected.chunkSize,
		chunks: cloneChunks(selected.chunks),
	}
}

func currentVersion(info *stripeInfo, version storage.StripeVersion) bool {
	return info != nil && info.version == version
}
