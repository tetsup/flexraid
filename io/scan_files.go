package io

import (
 "context"
 "os"
 "path/filepath"
 "github.com/tetsup/flexraid/storage"
)

func (layer *IO) walkFiles(ctx context.Context, include func(storage.FileKey)bool, callback func(ScanEntry)error)(int,error){
 count:=0
 err:=filepath.WalkDir(layer.root,func(path string,entry os.DirEntry,walkErr error)error{
  if walkErr!=nil{return walkErr}
  if err:=ctx.Err();err!=nil{return err}
  if entry.IsDir(){return nil}
  relative,err:=filepath.Rel(layer.root,path);if err!=nil{return err}
  key,err:=storage.ParseFileKey(filepath.ToSlash(relative))
  if err!=nil{layer.logger.Warn("skipping file with invalid name","path",relative,"error",err);return nil}
  if !include(key){return nil}
  data,err:=os.ReadFile(path);if err!=nil{return err}
  if err:=callback(ScanEntry{Key:key,Data:data});err!=nil{return err}
  count++;return nil
 })
 return count,err
}
