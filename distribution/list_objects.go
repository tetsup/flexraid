package distribution
import("context";"sort";"time";"github.com/tetsup/flexraid/pkg/shared")
type ListObjectsRequest struct{Context context.Context}
type ListObjectsResponse struct{ObjectIDs []shared.ObjectID}
func(d *Distribution) ListObjects(request ListObjectsRequest)(response ListObjectsResponse,err error){
	started:=time.Now()
	defer func(){logOperation(d,"ListObjects",started,err,"objects",len(response.ObjectIDs))}()
	ctx:=request.Context;if ctx==nil{ctx=context.Background()}
	if err=ctx.Err();err!=nil{return response,err}
	d.mapMu.Lock();defer d.mapMu.Unlock()
	for id,stripes:=range d.objects{if stripes[0]!=nil{response.ObjectIDs=append(response.ObjectIDs,id)}}
	sort.Slice(response.ObjectIDs,func(i,j int)bool{return response.ObjectIDs[i].String()<response.ObjectIDs[j].String()})
	return response,nil
}
