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
	d.mapMu.RLock()
	items := make([]cleanupFile, 0)
	seen := make(map[struct {
		ioIndex int
		key     storage.FileKey
	}]struct{})
	appendItem := func(location chunkLocation) {
		if location.readers != nil && location.readers.Load() > 0 {
			return
		}
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
	d.mapMu.RUnlock()

	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		unlock := d.lockObjectWrite(item.location.key.ObjectID)
		key := stripeIDKey{objectID: item.location.key.ObjectID, stripeID: item.location.key.StripeID}
		d.mapMu.RLock()
		current := d.objects[key.objectID][key.stripeID]
		isCurrent := currentVersion(current, item.location.key.StripeVersion)
		d.mapMu.RUnlock()
		if isCurrent && !request.InvalidStripes && !request.IncompleteChunks {
			unlock()
			continue
		}
		_, deleteErr := d.ios[item.location.ioIndex].Delete(physicalio.DeleteRequest{Context: ctx, Key: item.location.key})
		d.recordChunkOperation("Delete", deleteErr)
		if deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
			unlock()
			return response, deleteErr
		}

		// Reconcile only the exact FileKey that was deleted. A concurrent
		// foreground write may have published a newer immutable version.
		d.mapMu.Lock()
		d.removeCleanupLocationLocked(item.candidate, item.location)
		d.reselectStripeLocked(key)
		d.refreshStripeCleanupQueueLocked(key)
		d.mapMu.Unlock()
		unlock()
		response.DeletedFiles++
	}
	return response, nil
}

func (d *Distribution) reselectStripeLocked(key stripeIDKey) {
	if key.stripeID == 0 {
		d.reselectObjectStripesLocked(key.objectID)
		return
	}
	stripes := d.objects[key.objectID]
	if stripes == nil || stripes[0] == nil {
		if stripes != nil {
			delete(stripes, key.stripeID)
			if len(stripes) == 0 { delete(d.objects, key.objectID) }
		}
		return
	}
	watermark := stripes[0].version
	versions := d.versions[key]
	current := stripes[key.stripeID]
	if current != nil && current.version <= watermark {
		if selected := versions[current.version]; selected != nil && selected.recoverable {
			return
		}
	}
	var latest storage.StripeVersion
	var selected *versionInfo
	for versionNo, candidate := range versions {
		if versionNo > watermark || !candidate.recoverable { continue }
		if selected == nil || versionNo > latest { latest, selected = versionNo, candidate }
	}
	if selected == nil {
		delete(stripes, key.stripeID)
	} else {
		stripes[key.stripeID] = &stripeInfo{version:latest,method:selected.method,chunkSize:selected.chunkSize,chunks:cloneChunks(selected.chunks)}
	}
	d.refreshStripeCleanupQueueLocked(key)
}

// reselectObjectStripesLocked rebuilds all selected Stripe versions from one
// recoverable Stripe 0 watermark. The caller must hold mapMu.
func (d *Distribution) reselectObjectStripesLocked(objectID shared.ObjectID) {
	headerKey := stripeIDKey{objectID: objectID, stripeID: 0}
	headerVersions := d.versions[headerKey]
	var watermark storage.StripeVersion
	var headerInfo *versionInfo
	for versionNo, candidate := range headerVersions {
		if candidate.recoverable && (headerInfo == nil || versionNo > watermark) {
			watermark, headerInfo = versionNo, candidate
		}
	}
	if headerInfo == nil {
		delete(d.objects, objectID)
		return
	}
	stripes := d.objects[objectID]
	if stripes == nil {
		stripes = make(map[storage.StripeID]*stripeInfo)
		d.objects[objectID] = stripes
	} else {
		for stripeID := range stripes { delete(stripes, stripeID) }
	}
	stripes[0] = &stripeInfo{version: watermark, method: headerInfo.method, chunkSize: headerInfo.chunkSize, chunks: cloneChunks(headerInfo.chunks)}
	d.refreshStripeCleanupQueueLocked(headerKey)

	for key, versions := range d.versions {
		if key.objectID != objectID || key.stripeID == 0 { continue }
		var selected storage.StripeVersion
		var selectedInfo *versionInfo
		for versionNo, candidate := range versions {
			if versionNo > watermark || !candidate.recoverable { continue }
			if selectedInfo == nil || versionNo > selected {
				selected, selectedInfo = versionNo, candidate
			}
		}
		if selectedInfo == nil {
			delete(stripes, key.stripeID)
		} else {
			stripes[key.stripeID] = &stripeInfo{version:selected, method:selectedInfo.method, chunkSize:selectedInfo.chunkSize, chunks:cloneChunks(selectedInfo.chunks)}
		}
		d.refreshStripeCleanupQueueLocked(key)
	}
	if len(stripes) == 0 { delete(d.objects, objectID) }
}

func currentVersion(info *stripeInfo, version storage.StripeVersion) bool {
	return info != nil && info.version == version
}
