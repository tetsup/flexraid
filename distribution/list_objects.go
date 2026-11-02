package distribution

import (
	"context"
	"sort"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type ListObjectsRequest struct { Context context.Context }
type ListObjectsResponse struct { ObjectIDs []shared.ObjectID }

func (d *Distribution) ListObjects(request ListObjectsRequest) (response ListObjectsResponse, err error) {
	started:=time.Now()
	defer func(){logOperation(d.logger,"ListObjects",started,err,"objects",len(response.ObjectIDs))}()
	if err=request.Context.Err();err!=nil{return response,err}
	seen:=make(map[shared.ObjectID]struct{})
	for id:=range d.objects{seen[id]=struct{}{}}
	for key:=range d.versions{seen[key.objectID]=struct{}{}}
	for id:=range d.orphans{seen[id]=struct{}{}}
	for id:=range seen{response.ObjectIDs=append(response.ObjectIDs,id)}
	sort.Slice(response.ObjectIDs,func(i,j int)bool{return response.ObjectIDs[i].String()<response.ObjectIDs[j].String()})
	return response,nil
}
