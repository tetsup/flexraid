package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

type NewRequest struct { Context context.Context; Distribution *distribution.Distribution; LogLevel slog.Level; Logger *slog.Logger }
type NewResponse struct { Layer *Orchestration }

type Orchestration struct {
	distribution *distribution.Distribution
	logger *slog.Logger
	headers map[shared.ObjectID]header
	deletionQueue map[shared.ObjectID]struct{}
}

func New(request NewRequest)(NewResponse,error){
	logger:=request.Logger
	if logger==nil{logger=slog.New(slog.NewTextHandler(os.Stderr,&slog.HandlerOptions{Level:request.LogLevel}))}
	if request.Distribution==nil{return NewResponse{},fmt.Errorf("distribution is nil")}
	ctx:=request.Context
	if ctx==nil{ctx=context.Background()}
	if err:=ctx.Err();err!=nil{return NewResponse{},err}
	layer:=&Orchestration{distribution:request.Distribution,logger:logger,headers:make(map[shared.ObjectID]header),deletionQueue:make(map[shared.ObjectID]struct{})}
	objects,err:=request.Distribution.ListObjects(distribution.ListObjectsRequest{Context:ctx})
	if err!=nil{return NewResponse{},err}
	for _,objectID:=range objects.ObjectIDs{
		read,readErr:=request.Distribution.ReadStripe(distribution.ReadStripeRequest{Context:ctx,ObjectID:objectID,StripeID:0})
		if readErr!=nil{return NewResponse{},fmt.Errorf("read object header %s: %w",objectID.String(),readErr)}
		if len(read.Data)<headerSize{return NewResponse{},fmt.Errorf("%w: header stripe too short for %s",errInvalidHeader,objectID.String())}
		var objectHeader header
		if err:=objectHeader.UnmarshalBinary(read.Data[:headerSize]);err!=nil{return NewResponse{},fmt.Errorf("decode object header %s: %w",objectID.String(),err)}
		if objectHeader.ObjectID!=objectID{return NewResponse{},fmt.Errorf("%w: object ID mismatch",errInvalidHeader)}
		layer.headers[objectID]=objectHeader
		if objectHeader.Deleted{layer.deletionQueue[objectID]=struct{}{}}
	}
	logger.Debug("Orchestration initialized","objects",len(layer.headers),"deletion_queue",len(layer.deletionQueue))
	return NewResponse{Layer:layer},nil
}
