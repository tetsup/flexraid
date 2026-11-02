package orchestration

import (
	"context"
	"time"

	"github.com/tetsup/flexraid/distribution"
)

type CleanupRequest struct {
	Context context.Context
	OldVersions bool
	InvalidStripes bool
	IncompleteChunks bool
}
type CleanupResponse struct { DeletedObjects int; DeletedFiles int }

func(layer *Orchestration)Cleanup(request CleanupRequest)(response CleanupResponse,err error){
	started:=time.Now()
	defer func(){logCleanup(layer.logger,started,err,response)}()
	for objectID:=range layer.deletionQueue{
		if err=request.Context.Err();err!=nil{return response,err}
		result,deleteErr:=layer.distribution.DeleteObject(distribution.DeleteObjectRequest{Context:request.Context,ObjectID:objectID})
		response.DeletedFiles+=result.DeletedFiles
		if deleteErr!=nil{err=deleteErr;return response,err}
		delete(layer.deletionQueue,objectID)
		delete(layer.headers,objectID)
		response.DeletedObjects++
	}
	result,err:=layer.distribution.Cleanup(distribution.CleanupRequest{Context:request.Context,OldVersions:request.OldVersions,InvalidStripes:request.InvalidStripes,IncompleteChunks:request.IncompleteChunks})
	response.DeletedFiles+=result.DeletedFiles
	return response,err
}

func logCleanup(logger interface{ Debug(string,...any) },started time.Time,err error,response CleanupResponse){
	attrs:=[]any{"duration",time.Since(started),"deleted_objects",response.DeletedObjects,"deleted_files",response.DeletedFiles}
	if err!=nil{attrs=append(attrs,"error",err)}
	logger.Debug("Orchestration cleanup completed",attrs...)
}
