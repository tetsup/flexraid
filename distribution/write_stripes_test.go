package distribution

import (
	"bytes"
	"context"
	"testing"
	"strings"

	"github.com/tetsup/flexraid/storage"
)

func TestWriteStripesCommitsOneObjectVersion(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil { t.Fatal(err) }
	id := testObjectID()
	layout := Layout{Type: LayoutMirror, NumChunks: 2}
	request := WriteStripesRequest{Context: ctx, ObjectID: id, Stripes: []WriteStripeRequest{
		{ObjectID:id, StripeID:1, ChunkSize:32, Layout:layout, IOIndexes:[]int{0,1}, Data:[]byte("data-one")},
		{ObjectID:id, StripeID:0, ChunkSize:32, Layout:layout, IOIndexes:[]int{0,1}, Data:[]byte("header-one")},
	}}
	committed, err := created.Layer.WriteStripes(request)
	if err != nil { t.Fatal(err) }
	if committed.Version != 1 { t.Fatalf("commit version = %d, want 1", committed.Version) }
	for _, stripeID := range []storage.StripeID{0,1} {
		info, err := created.Layer.GetStripeInfo(GetStripeInfoRequest{ObjectID:id, StripeID:stripeID})
		if err != nil { t.Fatal(err) }
		if info.Version != committed.Version { t.Errorf("stripe %d version = %d, want %d", stripeID, info.Version, committed.Version) }
	}
	restarted, err := New(NewRequest{IOs:ios})
	if err != nil { t.Fatal(err) }
	for stripeID, want := range map[storage.StripeID][]byte{0:[]byte("header-one"),1:[]byte("data-one")} {
		read, err := restarted.Layer.ReadStripe(ReadStripeRequest{Context:ctx,ObjectID:id,StripeID:stripeID,ChunkSize:32})
		if err != nil { t.Fatal(err) }
		if read.Version != committed.Version || !bytes.Equal(read.Data,want) { t.Errorf("stripe %d = version %d data %q; want version %d data %q",stripeID,read.Version,read.Data,committed.Version,want) }
	}
}

func TestUncommittedStripeIsIgnoredAndRemovedBeforeNextCommit(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t,2)
	created,err := New(NewRequest{IOs:ios})
	if err != nil { t.Fatal(err) }
	id:=testObjectID()
	layout:=Layout{Type:LayoutMirror,NumChunks:2}
	write := func(version storage.StripeVersion, header,data string) {
		t.Helper()
		_,err:=created.Layer.WriteStripes(WriteStripesRequest{Context:ctx,ObjectID:id,Stripes:[]WriteStripeRequest{
			{ObjectID:id,StripeID:1,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte(data)},
			{ObjectID:id,StripeID:0,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte(header)},
		}})
		if err!=nil { t.Fatal(err) }
	}
	write(1,"header-v1","data-v1")
	// Simulate a crash after a new data stripe has reached disk but before
	// Stripe 0 advances its watermark.
	_,err=created.Layer.writeStripeAtVersion(WriteStripeRequest{Context:ctx,ObjectID:id,StripeID:1,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte("uncommitted")},2,false)
	if err!=nil { t.Fatal(err) }
	restarted,err:=New(NewRequest{IOs:ios})
	if err!=nil { t.Fatal(err) }
	read,err:=restarted.Layer.ReadStripe(ReadStripeRequest{Context:ctx,ObjectID:id,StripeID:1,ChunkSize:32})
	if err!=nil { t.Fatal(err) }
	if string(read.Data)!="data-v1" || read.Version!=1 { t.Fatalf("restored uncommitted data: version=%d data=%q",read.Version,read.Data) }
	_,err=restarted.Layer.WriteStripes(WriteStripesRequest{Context:ctx,ObjectID:id,Stripes:[]WriteStripeRequest{
		{ObjectID:id,StripeID:1,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte("data-v2")},
		{ObjectID:id,StripeID:0,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte("header-v2")},
	}})
	if err!=nil { t.Fatal(err) }
	final,err:=New(NewRequest{IOs:ios})
	if err!=nil { t.Fatal(err) }
	read,err=final.Layer.ReadStripe(ReadStripeRequest{Context:ctx,ObjectID:id,StripeID:1,ChunkSize:32})
	if err!=nil { t.Fatal(err) }
	if read.Version!=2 || string(read.Data)!="data-v2" { t.Fatalf("final data: version=%d data=%q",read.Version,read.Data) }
}

func TestWriteStripesRequiresStripeZero(t *testing.T) {
	created,err:=New(NewRequest{IOs:makeIOs(t,2)})
	if err!=nil { t.Fatal(err) }
	id := testObjectID()
	_,err=created.Layer.WriteStripes(WriteStripesRequest{Context:context.Background(),ObjectID:id,Stripes:[]WriteStripeRequest{
		{ObjectID:id,StripeID:4,ChunkSize:8,Layout:Layout{Type:LayoutMirror,NumChunks:2},IOIndexes:[]int{0,1},Data:[]byte("data")},
	}})
	if err==nil || !strings.Contains(err.Error(),"Stripe 0") { t.Fatalf("expected missing Stripe 0 error, got %v",err) }
}

func TestUnrecoverableStripeZeroFallsBackAndConstrainsDataVersions(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs:ios})
	if err != nil { t.Fatal(err) }
	id := testObjectID()
	layout := Layout{Type:LayoutMirror,NumChunks:2}
	commit := func(header,data string) storage.StripeVersion {
		t.Helper()
		result, err := created.Layer.WriteStripes(WriteStripesRequest{Context:ctx,ObjectID:id,Stripes:[]WriteStripeRequest{
			{ObjectID:id,StripeID:1,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte(data)},
			{ObjectID:id,StripeID:0,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte(header)},
		}})
		if err != nil { t.Fatal(err) }
		return result.Version
	}
	v1 := commit("header-v1","data-v1")
	v2 := commit("header-v2","data-v2")
	created.Layer.markStripeUnrecoverable(stripeIDKey{objectID:id,stripeID:0},v2)
	headerInfo,err:=created.Layer.GetStripeInfo(GetStripeInfoRequest{ObjectID:id,StripeID:0})
	if err!=nil { t.Fatal(err) }
	dataInfo,err:=created.Layer.GetStripeInfo(GetStripeInfoRequest{ObjectID:id,StripeID:1})
	if err!=nil { t.Fatal(err) }
	if headerInfo.Version!=v1 || dataInfo.Version!=v1 { t.Fatalf("fallback versions: header=%d data=%d, want %d",headerInfo.Version,dataInfo.Version,v1) }
	read,err:=created.Layer.ReadStripe(ReadStripeRequest{Context:ctx,ObjectID:id,StripeID:1,ChunkSize:32})
	if err!=nil { t.Fatal(err) }
	if read.Version!=v1 || string(read.Data)!="data-v1" { t.Fatalf("fallback read: version=%d data=%q",read.Version,read.Data) }
}

func TestCommitDoesNotActivateUncommittedVersionOnUnchangedStripe(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs:ios})
	if err != nil { t.Fatal(err) }
	id := testObjectID()
	layout := Layout{Type:LayoutMirror,NumChunks:2}
	commit := func(stripes ...WriteStripeRequest) {
		t.Helper()
		_, err := created.Layer.WriteStripes(WriteStripesRequest{Context:ctx,ObjectID:id,Stripes:stripes})
		if err != nil { t.Fatal(err) }
	}
	makeStripe := func(stripeID storage.StripeID, data string) WriteStripeRequest {
		return WriteStripeRequest{ObjectID:id,StripeID:stripeID,ChunkSize:32,Layout:layout,IOIndexes:[]int{0,1},Data:[]byte(data)}
	}
	commit(
		makeStripe(0,"header-v1"),
		makeStripe(1,"data-1-v1"),
		makeStripe(2,"data-2-v1"),
	)
	// A failed earlier transaction left Version 2 on Stripe 2, which this
	// transaction does not otherwise need to update.
	_, err = created.Layer.writeStripeAtVersion(WriteStripeRequest{
		Context:ctx,ObjectID:id,StripeID:2,ChunkSize:32,Layout:layout,
		IOIndexes:[]int{0,1},Data:[]byte("stale-data-2-v2"),
	},2,false)
	if err != nil { t.Fatal(err) }

	commit(
		makeStripe(0,"header-v2"),
		makeStripe(1,"data-1-v2"),
	)
	restarted, err := New(NewRequest{IOs:ios})
	if err != nil { t.Fatal(err) }
	read, err := restarted.Layer.ReadStripe(ReadStripeRequest{Context:ctx,ObjectID:id,StripeID:2,ChunkSize:32})
	if err != nil { t.Fatal(err) }
	if read.Version != 1 || string(read.Data) != "data-2-v1" {
		t.Fatalf("unchanged stripe was advanced by stale candidate: version=%d data=%q",read.Version,read.Data)
	}
}

func TestListObjectsHidesUncommittedObjectVersions(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t,2)
	created,err:=New(NewRequest{IOs:ios})
	if err!=nil { t.Fatal(err) }
	id:=testObjectID()
	_,err=created.Layer.writeStripeAtVersion(WriteStripeRequest{
		Context:ctx,ObjectID:id,StripeID:1,ChunkSize:32,
		Layout:Layout{Type:LayoutMirror,NumChunks:2},IOIndexes:[]int{0,1},Data:[]byte("uncommitted"),
	},1,false)
	if err!=nil { t.Fatal(err) }
	listed,err:=created.Layer.ListObjects(ListObjectsRequest{Context:ctx})
	if err!=nil { t.Fatal(err) }
	for _,got:=range listed.ObjectIDs { if got==id { t.Fatal("uncommitted Object was visible in ListObjects") } }
}
