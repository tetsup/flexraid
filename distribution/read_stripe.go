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

type ReadStripeRequest struct {
	Context context.Context
	ObjectID shared.ObjectID
	StripeID storage.StripeID
	RepairIOIndexes []int
}
type ReadStripeResponse struct { Data []byte; Version storage.StripeVersion }

func (d *Distribution) ReadStripe(request ReadStripeRequest)(response ReadStripeResponse,err error){
	started:=time.Now()
	defer func(){logOperation(d.logger,"ReadStripe",started,err,"object_id",request.ObjectID.String(),"stripe_id",request.StripeID,"bytes",len(response.Data))}()
	if err=request.Context.Err();err!=nil{return response,err}
	stripes:=d.objects[request.ObjectID];if stripes==nil||stripes[request.StripeID]==nil{return response,ErrStripeNotFound}
	info:=stripes[request.StripeID]
	key:=stripeKeyInfo{objectID:request.ObjectID,stripeID:request.StripeID,stripeVersion:info.version}
	chunks:=make([]chunkDataWithInfo,0,len(info.chunks))
	missing:=make([]chunkNo,0)
	for no,location:=range info.chunks{
		read,readErr:=d.ios[location.ioIndex].Read(physicalio.ReadRequest{Context:request.Context,Key:location.key})
		if readErr!=nil{
			d.recordIOError(location.ioIndex,readErr);missing=append(missing,no);continue
		}
		total,payload,decodeErr:=decodeChunk(read.Data)
		if decodeErr!=nil{d.recordIOError(location.ioIndex,decodeErr);missing=append(missing,no);continue}
		chunks=append(chunks,chunkDataWithInfo{stripeKey:key,distributionMethod:info.method,chunkNo:no,data:payload,totalLength:total})
	}
	restored,restoreErr:=restoreStripe(chunks)
	if restoreErr!=nil{
		info.corrupted=true;d.totalErrors++
		d.logger.Error("stripe data corrupted","object_id",request.ObjectID.String(),"stripe_id",request.StripeID,"error",restoreErr)
		return response,ErrDataCorrupted
	}
	response.Data=restored;response.Version=info.version
	if len(missing)>0{
		info.corrupted=true
		d.logger.Warn("stripe recovered from missing chunks","object_id",request.ObjectID.String(),"stripe_id",request.StripeID,"missing_chunks",len(missing))
		d.repairStripe(request,info,missing,restored)
	}
	return response,nil
}

func(d *Distribution)repairStripe(request ReadStripeRequest,info *stripeInfo,missing []chunkNo,data []byte){
	minimum:=1
	if info.method.distributionType==distributionParity{minimum=int(info.method.numChunks)}
	if validateIOIndexes(request.RepairIOIndexes,len(d.ios),minimum)!=nil{return}
	chunkData:=make(map[chunkNo][]byte)
	if info.method.distributionType==distributionMirror{
		for _,no:=range missing{chunkData[no]=append([]byte(nil),data...)}
	}else{
		chunkData=splitStripe(data,info.method.numChunks)
		if containsChunk(missing,parityFlag){chunkData[parityFlag]=generateParity(orderedDataChunks(chunkData,info.method.numChunks))}
	}
	next:=0
	for _,no:=range missing{
		payload,ok:=chunkData[no];if !ok{continue}
		fileKey:=storage.FileKey{ObjectID:request.ObjectID,StripeVersion:info.version,LayoutID:layoutFor(info.method,no),StripeID:request.StripeID}
		oldLocation,hadOld:=info.chunks[no]
		if hadOld { _,removeErr:=d.ios[oldLocation.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:oldLocation.key}); if removeErr!=nil&&!errors.Is(removeErr,os.ErrNotExist){d.recordIOError(oldLocation.ioIndex,removeErr);return} }
		written:=false
		for next<len(request.RepairIOIndexes){
			index:=request.RepairIOIndexes[next];next++
			_,err:=d.ios[index].Write(physicalio.WriteRequest{Context:request.Context,Key:fileKey,Data:encodeChunk(uint64(len(data)),payload)})
			if err==nil{location:=chunkLocation{ioIndex:index,key:fileKey,data:payload,totalLength:uint64(len(data))};info.chunks[no]=location;stripeKey:=stripeIDKey{objectID:request.ObjectID,stripeID:request.StripeID};if versions:=d.versions[stripeKey];versions!=nil{if version:=versions[info.version];version!=nil{version.chunks[no]=location}};written=true;break}
			if !errors.Is(err,os.ErrExist){d.recordIOError(index,err)}
		}
		if !written{return}
	}
	info.corrupted=false
}
func containsChunk(chunks []chunkNo,target chunkNo)bool{for _,no:=range chunks{if no==target{return true}};return false}
