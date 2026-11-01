package distribution

import (
 "bytes"
 "fmt"
 "sort"
)

// restoreStripe validates chunk identity and dispatches to the selected layout.
func restoreStripe(chunks []chunkDataWithInfo)([]byte,error){
 if len(chunks)==0{return nil,ErrInsufficientChunks}
 method:=chunks[0].DistributionMethod
 if err:=validateDistributionMethod(method);err!=nil{return nil,err}
 switch method.DistributionType {
 case DistributionMirror: return restoreMirror(chunks,method)
 case DistributionParity: return restoreParity(chunks,method)
 default: return nil,ErrInvalidDistributionMethod
 }
}

func validateDistributionMethod(method DistributionMethod)error{
 if method.NumChunks==0||method.NumChunks>127{return ErrInvalidDistributionMethod}
 switch method.DistributionType{
 case DistributionMirror:
  if method.NumParities!=0{return ErrInvalidDistributionMethod}
 case DistributionParity:
  if method.NumParities!=1{return ErrInvalidDistributionMethod}
 default:return ErrInvalidDistributionMethod
 }
 return nil
}

func validateChunks(chunks []chunkDataWithInfo,method DistributionMethod)(map[ChunkNo][]byte,error){
 var key StripeKeyInfo
 result:=make(map[ChunkNo][]byte,len(chunks))
 for i,chunk:=range chunks{
  if chunk.StripeKey.ObjectID==0{return nil,fmt.Errorf("%w: zero object ID",ErrInvalidChunk)}
  if i==0{key=chunk.StripeKey}else if chunk.StripeKey!=key{return nil,fmt.Errorf("%w: mixed stripe versions",ErrInvalidChunk)}
  if chunk.DistributionMethod!=method{return nil,fmt.Errorf("%w: mixed distribution methods",ErrInvalidChunk)}
  if _,exists:=result[chunk.ChunkNo];exists{return nil,fmt.Errorf("%w: duplicate chunk number %d",ErrInvalidChunk,chunk.ChunkNo)}
  if chunk.ChunkNo.IsParity() {
   if method.DistributionType!=DistributionParity || chunk.ChunkNo.Index()>=method.NumParities { return nil,fmt.Errorf("%w: parity chunk index %d out of range",ErrInvalidChunk,chunk.ChunkNo.Index()) }
  } else if chunk.ChunkNo.Index()>=method.NumChunks {
   return nil,fmt.Errorf("%w: data chunk index %d out of range",ErrInvalidChunk,chunk.ChunkNo.Index())
  }
  result[chunk.ChunkNo]=chunk.Data
 }
 return result,nil
}

func restoreMirror(chunks []chunkDataWithInfo,method DistributionMethod)([]byte,error){
 if err:=validateDistributionMethod(method);err!=nil||method.DistributionType!=DistributionMirror{return nil,ErrInvalidDistributionMethod}
 indexed,err:=validateChunks(chunks,method);if err!=nil{return nil,err}
 indexes:=make([]int,0,len(indexed))
 for no:=range indexed{if no.IsParity(){return nil,ErrInvalidChunk};indexes=append(indexes,int(no.Index()))}
 if len(indexes)==0{return nil,ErrInsufficientChunks}
 sort.Ints(indexes)
 // Any available mirror replica contains the complete stripe.
 return append([]byte(nil),indexed[ChunkNo(indexes[0])]...),nil
}

func restoreParity(chunks []chunkDataWithInfo,method DistributionMethod)([]byte,error){
 if err:=validateDistributionMethod(method);err!=nil||method.DistributionType!=DistributionParity{return nil,ErrInvalidDistributionMethod}
 indexed,err:=validateChunks(chunks,method);if err!=nil{return nil,err}
 data:=make([][]byte,method.NumChunks)
 missing:=-1
 for i:=range data{
  if value,ok:=indexed[ChunkNo(i)];ok{data[i]=value}else if missing==-1{missing=i}else{return nil,ErrTooManyMissingChunks}
 }
 if missing>=0{
  parity,ok:=indexed[ParityFlag]
  if !ok{return nil,ErrInsufficientChunks}
  restored:=append([]byte(nil),parity...)
  for i,chunk:=range data{if i==missing{continue};for j,b:=range chunk{if j>=len(restored){return nil,ErrInconsistentParity};restored[j]^=b}}
  data[missing]=restored
 }
 // If all data chunks are present, parity is optional. If it is present,
 // verify it so corrupted parity is not silently accepted.
 if parity,ok:=indexed[ParityFlag];ok{
  expected:=generateParity(data)
  if !bytes.Equal(expected,parity){return nil,ErrInconsistentParity}
 }
 return combineStripe(data),nil
}

func generateParity(chunks [][]byte)[]byte{
 max:=0;for _,chunk:=range chunks{if len(chunk)>max{max=len(chunk)}}
 parity:=make([]byte,max)
 for _,chunk:=range chunks{for i,b:=range chunk{parity[i]^=b}}
 return parity
}

func combineStripe(chunks [][]byte)[]byte{
 total:=0;for _,chunk:=range chunks{total+=len(chunk)}
 result:=make([]byte,0,total);for _,chunk:=range chunks{result=append(result,chunk...)}
 return result
}
