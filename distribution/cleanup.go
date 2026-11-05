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

func (d *Distribution) Cleanup(request CleanupRequest) (response CleanupResponse, err error) {
	started := time.Now()
	defer func() { logOperation(d.logger, "Cleanup", started, err, "deleted_files", response.DeletedFiles) }()
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	excluded := make(map[shared.ObjectID]struct{}, len(request.ExcludeObjectIDs))
	for _, id := range request.ExcludeObjectIDs {
		excluded[id] = struct{}{}
	}
	if request.IncompleteChunks || request.InvalidStripes || request.OldVersions {
		for objectID, locations := range d.orphans {
			if _, skip := excluded[objectID]; skip {
				continue
			}
			remaining := locations[:0]
			for _, location := range locations {
				current := d.objects[objectID][location.key.StripeID]
				remove := request.InvalidStripes || request.IncompleteChunks || (request.OldVersions && (current == nil || location.key.StripeVersion < current.version))
				if !remove {
					remaining = append(remaining, location)
					continue
				}
				if err = request.Context.Err(); err != nil {
					return response, err
				}
				_, deleteErr := d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context: request.Context, Key: location.key})
				if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
					remaining = append(remaining, location)
					d.orphans[objectID] = remaining
					err = deleteErr
					return response, err
				}
				response.DeletedFiles++
			}
			if len(remaining) == 0 {
				delete(d.orphans, objectID)
			} else {
				d.orphans[objectID] = remaining
			}
		}
	}
	for key, versions := range d.versions {
		if _, skip := excluded[key.objectID]; skip {
			continue
		}
		current := d.objects[key.objectID][key.stripeID]
		for versionNo, version := range versions {
			isCurrent := currentVersion(current, versionNo)
			removeVersion := request.OldVersions && !isCurrent || request.InvalidStripes && !version.recoverable
			if !removeVersion && !request.IncompleteChunks {
				continue
			}
			for no, location := range version.chunks {
				if !removeVersion && version.recoverable {
					continue
				}
				if err = request.Context.Err(); err != nil {
					return response, err
				}
				_, deleteErr := d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context: request.Context, Key: location.key})
				if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
					err = deleteErr
					return response, err
				}
				response.DeletedFiles++
				delete(version.chunks, no)
			}
			if removeVersion || len(version.chunks) == 0 {
				delete(versions, versionNo)
			}
		}
		if len(versions) == 0 {
			delete(d.versions, key)
		}
		if current != nil {
			if selected := d.versions[key][current.version]; selected == nil {
				var latest storage.StripeVersion
				var latestInfo *versionInfo
				for versionNo, candidate := range d.versions[key] {
					if candidate.recoverable && (latestInfo == nil || versionNo > latest) {
						latest, latestInfo = versionNo, candidate
					}
				}
				if latestInfo == nil {
					delete(d.objects[key.objectID], key.stripeID)
				} else {
					d.objects[key.objectID][key.stripeID] = &stripeInfo{version: latest, method: latestInfo.method, chunks: cloneChunks(latestInfo.chunks)}
				}
			}
		}
	}
	return response, nil
}
func currentVersion(info *stripeInfo, version storage.StripeVersion) bool {
	return info != nil && info.version == version
}
