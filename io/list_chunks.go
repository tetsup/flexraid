package io
import("context";"errors";"time")
type ListChunksRequest struct{Context context.Context;Callback func(ScanEntry)error}
type ListChunksResponse struct{Entries int}
// ListChunks enumerates every physical chunk file without interpreting its content.
func(layer *IO)ListChunks(req ListChunksRequest)(res ListChunksResponse,err error){
 started:=operationStarted()
 defer func(){logResult(layer.logger,"ListChunks",err,"duration",time.Since(started),"entries",res.Entries)}()
 if req.Callback==nil{return res,errors.New("list chunks callback is nil")}
 if err=req.Context.Err();err!=nil{return res,err}
 res.Entries,err=layer.walkFiles(req.Context,func(storage.FileKey)bool{return true},req.Callback)
 return res,err
}
