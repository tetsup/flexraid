package distribution

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type NewRequest struct { Context context.Context; IOs []*physicalio.IO; LogLevel slog.Level; Logger *slog.Logger }
type NewResponse struct { Layer *Distribution }

type Distribution struct {
	ios []*physicalio.IO
	logger *slog.Logger
	objects map[shared.ObjectID]map[storage.StripeID]*stripeInfo
	versions map[stripeIDKey]map[storage.StripeVersion]*versionInfo
	ioErrors []uint64
	totalErrors uint64
	ioFailures []error
	orphans map[shared.ObjectID][]chunkLocation
}
type stripeIDKey struct { objectID shared.ObjectID; stripeID storage.StripeID }
type stripeInfo struct { version storage.StripeVersion; method distributionMethod; chunks map[chunkNo]chunkLocation; corrupted bool }
type versionInfo struct { method distributionMethod; chunks map[chunkNo]chunkLocation; recoverable bool }
type chunkLocation struct { ioIndex int; key storage.FileKey; data []byte; totalLength uint64 }

func New(request NewRequest) (NewResponse, error) {
	logger := request.Logger
	if logger == nil { logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: request.LogLevel})) }
	ctx:=request.Context
	if ctx==nil{ctx=context.Background()}
	if len(request.IOs) == 0 { return NewResponse{}, errNoUsableIO }
	seen := make(map[*physicalio.IO]struct{}, len(request.IOs))
	for _, layer := range request.IOs {
		if layer == nil { return NewResponse{}, fmt.Errorf("nil I/O layer") }
		if _, ok := seen[layer]; ok { return NewResponse{}, fmt.Errorf("duplicate I/O layer") }
		seen[layer] = struct{}{}
	}
	d := &Distribution{ios: append([]*physicalio.IO(nil), request.IOs...), logger: logger,
		objects: make(map[shared.ObjectID]map[storage.StripeID]*stripeInfo),
		versions: make(map[stripeIDKey]map[storage.StripeVersion]*versionInfo),
		ioErrors: make([]uint64, len(request.IOs)), ioFailures: make([]error, len(request.IOs)), orphans: make(map[shared.ObjectID][]chunkLocation)}
	usable := 0
	for index, layer := range d.ios {
		scanResult, err := layer.ListChunks(physicalio.ListChunksRequest{Context: ctx, Callback: func(entry physicalio.ScanEntry) error {
			if entry.Error != nil { d.recordIOError(index, entry.Error); d.orphans[entry.Key.ObjectID]=append(d.orphans[entry.Key.ObjectID],chunkLocation{ioIndex:index,key:entry.Key}); return nil }
			method, no, methodErr := methodFromLayout(entry.Key.LayoutID)
			if methodErr != nil { d.recordIOError(index, methodErr); d.orphans[entry.Key.ObjectID]=append(d.orphans[entry.Key.ObjectID],chunkLocation{ioIndex:index,key:entry.Key}); return nil }
			total, payload, decodeErr := decodeChunk(entry.Data)
			if decodeErr != nil { d.recordIOError(index, decodeErr); d.orphans[entry.Key.ObjectID]=append(d.orphans[entry.Key.ObjectID],chunkLocation{ioIndex:index,key:entry.Key}); return nil }
			key := stripeIDKey{objectID: entry.Key.ObjectID, stripeID: entry.Key.StripeID}
			versions := d.versions[key]
			if versions == nil { versions = make(map[storage.StripeVersion]*versionInfo); d.versions[key] = versions }
			version := versions[entry.Key.StripeVersion]
			if version == nil { version = &versionInfo{method: method, chunks: make(map[chunkNo]chunkLocation)}; versions[entry.Key.StripeVersion] = version }
			if version.method != method { d.recordIOError(index, errInvalidChunk); d.orphans[entry.Key.ObjectID]=append(d.orphans[entry.Key.ObjectID],chunkLocation{ioIndex:index,key:entry.Key}); return nil }
			if _, exists := version.chunks[no]; exists { d.recordIOError(index, fmt.Errorf("%w: duplicate chunk", errInvalidChunk)); d.orphans[entry.Key.ObjectID]=append(d.orphans[entry.Key.ObjectID],chunkLocation{ioIndex:index,key:entry.Key}); return nil }
			version.chunks[no] = chunkLocation{ioIndex: index, key: entry.Key, data: payload, totalLength: total}
			return nil
		}})
		if ctx.Err()!=nil{return NewResponse{},ctx.Err()}
		if err != nil { d.ioFailures[index] = err; d.recordIOError(index, err); if scanResult.Entries>0{usable++}; continue }
		usable++
	}
	if usable == 0 { return NewResponse{}, errNoUsableIO }
	for key, versions := range d.versions {
		var selected storage.StripeVersion
		var selectedInfo *versionInfo
		for versionNo, version := range versions {
			chunks := make([]chunkDataWithInfo, 0, len(version.chunks))
			for no, location := range version.chunks {
				chunks = append(chunks, chunkDataWithInfo{stripeKey:stripeKeyInfo{objectID:key.objectID,stripeID:key.stripeID,stripeVersion:versionNo}, distributionMethod:version.method, chunkNo:no, data:location.data, totalLength:location.totalLength})
			}
			if _, err := restoreStripe(chunks); err != nil { continue }
			version.recoverable = true
			if selectedInfo == nil || versionNo > selected { selected, selectedInfo = versionNo, version }
		}
		if selectedInfo != nil {
			stripes := d.objects[key.objectID]
			if stripes == nil { stripes = make(map[storage.StripeID]*stripeInfo); d.objects[key.objectID] = stripes }
			stripes[key.stripeID] = &stripeInfo{version:selected, method:selectedInfo.method, chunks:cloneChunks(selectedInfo.chunks)}
		}
	}
	logger.Debug("Distribution initialized", "ios",len(d.ios),"usable_ios",usable,"objects",len(d.objects))
	return NewResponse{Layer:d}, nil
}

func methodFromLayout(layout storage.LayoutID) (distributionMethod, chunkNo, error) {
	var method distributionMethod
	switch layout.Type {
	case storage.LayoutType(LayoutMirror):
		method = distributionMethod{distributionType:distributionMirror,numChunks:layout.DriveCount}
		if layout.Role != storage.ChunkRoleData || layout.Index == 0 { return method,0,errInvalidChunk }
		return method,chunkNo(layout.Index-1),nil
	case storage.LayoutType(LayoutParity):
		if layout.DriveCount < 2 { return method,0,errInvalidChunk }
		method = distributionMethod{distributionType:distributionParity,numChunks:layout.DriveCount-1,numParities:1}
		if layout.Role == storage.ChunkRoleParity { if layout.Index!=1{return method,0,errInvalidChunk}; return method,parityFlag,nil }
		if layout.Role != storage.ChunkRoleData || layout.Index == 0 { return method,0,errInvalidChunk }
		return method,chunkNo(layout.Index-1),nil
	default: return method,0,errInvalidDistributionMethod
	}
}

func layoutFor(method distributionMethod,no chunkNo) storage.LayoutID {
	count:=method.numChunks
	if method.distributionType==distributionParity { count++ }
	role,index:=storage.ChunkRoleData,no.index()+1
	if no.isParity() { role,index=storage.ChunkRoleParity,1 }
	typ:=storage.LayoutType(LayoutMirror)
	if method.distributionType==distributionParity { typ=storage.LayoutType(LayoutParity) }
	return storage.LayoutID{Type:typ,DriveCount:count,Role:role,Index:index}
}
func encodeChunk(total uint64,data []byte) []byte { result:=make([]byte,8+len(data));binary.BigEndian.PutUint64(result[:8],total);copy(result[8:],data);return result }
func decodeChunk(data []byte)(uint64,[]byte,error){if len(data)<8{return 0,nil,errInvalidChunk};return binary.BigEndian.Uint64(data[:8]),append([]byte(nil),data[8:]...),nil}
func cloneChunks(source map[chunkNo]chunkLocation)map[chunkNo]chunkLocation{result:=make(map[chunkNo]chunkLocation,len(source));for no,loc:=range source{loc.data=append([]byte(nil),loc.data...);result[no]=loc};return result}
func(d *Distribution)recordIOError(index int,err error){if index>=0&&index<len(d.ioErrors){d.ioErrors[index]++};d.totalErrors++;d.logger.Warn("distribution I/O error","io_index",index,"error",err)}
func(d *Distribution)nextVersion(key stripeIDKey)storage.StripeVersion{var max storage.StripeVersion;for version:=range d.versions[key]{if version>max{max=version}};for _,location:=range d.orphans[key.objectID]{if location.key.StripeID==key.stripeID&&location.key.StripeVersion>max{max=location.key.StripeVersion}};return max+1}
