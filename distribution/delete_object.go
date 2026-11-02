package distribution

import (
	"context"
	"errors"
	"os"
	"sort"
	"time"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
)

type DeleteObjectRequest struct { Context context.Context; ObjectID shared.ObjectID }
type DeleteObjectResponse struct { DeletedFiles int }

func(d *Distribution)DeleteObject(request DeleteObjectRequest)(response DeleteObjectResponse,err error){
	started:=time.Now();defer func(){logOperation(d.logger,"DeleteObject",started,err,"object_id",request.ObjectID.String(),"deleted_files",response.DeletedFiles)}()
	if err=request.Context.Err();err!=nil{return response,err}
	orphans:=d.orphans[request.ObjectID]
	for _,location:=range orphans { if location.key.StripeID!=0 { _,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key}); if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}; response.DeletedFiles++ } }
	keys:=make([]stripeKeyInfo,0)
	for key:=range d.versions{if key.objectID==request.ObjectID&&key.stripeID!=0{keys=append(keys,key)}}
	sort.Slice(keys,func(i,j int)bool{if keys[i].stripeID!=keys[j].stripeID{return keys[i].stripeID<keys[j].stripeID};return keys[i].stripeVersion<keys[j].stripeVersion})
	for _,key:=range keys{
		if err=request.Context.Err();err!=nil{return response,err}
		version:=d.versions[key][key.stripeVersion]
		for _,location:=range version.chunks{
			_,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key})
			if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}
			response.DeletedFiles++
		}
		delete(d.versions[key],key.stripeVersion);if len(d.versions[key])==0{delete(d.versions,key)}
	}
	for _,location:=range orphans { if location.key.StripeID==0 { _,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key}); if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}; response.DeletedFiles++ } }
	delete(d.orphans,request.ObjectID)
	headerKeys:=make([]stripeKeyInfo,0)
	for key:=range d.versions{if key.objectID==request.ObjectID&&key.stripeID==0{headerKeys=append(headerKeys,key)}}
	for _,key:=range headerKeys{
		for _,location:=range d.versions[key][key.stripeVersion].chunks{
			_,deleteErr:=d.ios[location.ioIndex].Delete(physicalio.DeleteRequest{Context:request.Context,Key:location.key})
			if deleteErr!=nil&&!errors.Is(deleteErr,os.ErrNotExist){err=deleteErr;return response,err}
			response.DeletedFiles++
		}
		delete(d.versions,key)
	}
	delete(d.objects,request.ObjectID)
	return response,nil
}
