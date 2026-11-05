package orchestration

import (
	"fmt"
	"path"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared"
)

var errNotDirectory = fmt.Errorf("not a directory")

// resolvePath resolves an absolute logical path from the restored root.
func (layer *Orchestration) resolvePath(value string) (*runtimeObject, error) {
	if layer.root == nil || value == "" || !strings.HasPrefix(value, "/") {
		return nil, fmt.Errorf("path must be absolute: %q", value)
	}
	clean := path.Clean(value)
	current := layer.root
	if clean == "/" { return current, nil }
	for _, component := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if current.header.Kind != objectKindDirectory {
			return nil, fmt.Errorf("component %q: %w", component, errNotDirectory)
		}
		next, exists := current.children[component]
		if !exists { return nil, fmt.Errorf("path %q: %w", value, errPathNotFound) }
		current = next
	}
	return current, nil
}

// resolveObjectID resolves an absolute path and returns its ObjectID.
func (layer *Orchestration) resolveObjectID(value string) (shared.ObjectID, error) {
	object, err := layer.resolvePath(value)
	if err != nil { return shared.ObjectID{}, err }
	return object.header.ObjectID, nil
}
