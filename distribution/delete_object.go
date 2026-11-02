package distribution

import (
	"context"
	"errors"
	"os"
	"sort"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type DeleteObjectRequest struct { Context context.Context; ObjectID shared.ObjectID }
type DeleteObjectResponse struct { DeletedFiles int }

func(d *Distribution)DeleteObject(request DeleteObjectRequest)(response DeleteObjectResponse,err error){
	started:=time.Now();defer func(){logOperation(d.logger,"DeleteObject",started,err,"object_id",request.ObjectID.String(),"deleted_files",response.DeletedFiles)}()
	if err=request.Context.Err();err!=nil{return response,err}
	orphans:=d.orphans[request.ObjectID]
	for _,location:=range orphans { if location.key.StripeID!=0 { _,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key}); if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}; response.DeletedFiles++ } }
	type targetVersion struct{ key stripeIDKey; version storage.StripeVersion }
	keys:=make([]targetVersion,0)
	for key,versions:=range d.versions{if key.objectID==request.ObjectID&&key.stripeID!=0{for version:=range versions{keys=append(keys,targetVersion{key:key,version:version})}}}
	sort.Slice(keys,func(i,j int)bool{if keys[i].key.stripeID!=keys[j].key.stripeID{return keys[i].key.stripeID<keys[j].key.stripeID};return keys[i].version<keys[j].version})
	for _,target:=range keys{
		if err=request.Context.Err();err!=nil{return response,err}
		version:=d.versions[target.key][target.version]
		for _,location:=range version.chunks{
			_,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key})
			if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}
			response.DeletedFiles++
		}
		delete(d.versions[target.key],target.version);if len(d.versions[target.key])==0{delete(d.versions,target.key)}
	}
	for _,location:=range orphans { if location.key.StripeID==0 { _,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key}); if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}; response.DeletedFiles++ } }
	delete(d.orphans,request.ObjectID)
	headerKeys:=make([]targetVersion,0)
	for key,versions:=range d.versions{if key.objectID==request.ObjectID&&key.stripeID==0{for version:=range versions{headerKeys=append(headerKeys,targetVersion{key:key,version:version})}}}
	sort.Slice(headerKeys,func(i,j int)bool{return headerKeys[i].version<headerKeys[j].version})
	for _,target:=range headerKeys{
		for _,location:=range d.versions[target.key][target.version].chunks{
			_,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key})
			if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}
			response.DeletedFiles++
		}
		delete(d.versions[target.key],target.version);if len(d.versions[target.key])==0{delete(d.versions,target.key)}
	}
	delete(d.objects,request.ObjectID)
	return response,nil
}
