package orchestration

import (
	"context"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestStartupRebuildsDeletionQueueAndCleanupDeletesObject(t *testing.T){
	ctx:=context.Background()
	ios:=[]*physicalio.IO{
		physicalio.New(physicalio.NewRequest{Root:t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root:t.TempDir()}).Layer,
	}
	initial,err:=distribution.New(distribution.NewRequest{IOs:ios});if err!=nil{t.Fatal(err)}
	id:=shared.ObjectID{0x12,0x34,0x56,0x78,0x9a,0xbc,0x4d,0xef,0x8a,0xbc,0x12,0x34,0x56,0x78,0x90,0xab}
	h:=header{FormatVersion:headerFormatVersion,Deleted:true,ObjectID:id,Kind:objectKindDirectory,Layout:layout{Mode:layoutModeMirror}}
	headerData,err:=h.MarshalBinary();if err!=nil{t.Fatal(err)}
	for _,item:=range []struct{stripe storage.StripeID;data []byte}{{0,headerData},{1,[]byte("payload")}}{
		if _,err:=initial.Layer.WriteStripe(distribution.WriteStripeRequest{Context:ctx,ObjectID:id,StripeID:item.stripe,Layout:distribution.Layout{Type:distribution.LayoutMirror,NumChunks:2},IOIndexes:[]int{0,1},Data:item.data});err!=nil{t.Fatal(err)}
	}
	restarted,err:=distribution.New(distribution.NewRequest{IOs:ios});if err!=nil{t.Fatal(err)}
	started,err:=New(NewRequest{Context:ctx,Distribution:restarted.Layer});if err!=nil{t.Fatal(err)}
	if len(started.Layer.deletionQueue)!=1{t.Fatalf("deletion queue has %d entries",len(started.Layer.deletionQueue))}
	cleaned,err:=started.Layer.Cleanup(CleanupRequest{Context:ctx});if err!=nil{t.Fatal(err)}
	if cleaned.DeletedObjects!=1{t.Fatalf("deleted objects=%d",cleaned.DeletedObjects)}
	objects,err:=restarted.Layer.ListObjects(distribution.ListObjectsRequest{Context:ctx});if err!=nil{t.Fatal(err)}
	if len(objects.ObjectIDs)!=0{t.Fatalf("objects remain: %v",objects.ObjectIDs)}
}
