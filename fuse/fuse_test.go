package fuse

import (
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

func testObjectID(t *testing.T) shared.ObjectID {
	t.Helper()
	id, err := shared.ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err != nil { t.Fatal(err) }
	return id
}

func TestInodeNumberIsStableAndNonzero(t *testing.T) {
	id := testObjectID(t)
	if first, second := inodeNumber(id), inodeNumber(id); first == 0 || first != second {
		t.Fatalf("inodeNumber is not stable and nonzero: first=%d second=%d", first, second)
	}
}

func TestStableAttrUsesTypeAndObjectID(t *testing.T) {
	id := testObjectID(t)
	got := stableAttr(orchestration.ObjectAttributes{ObjectID:id, Mode:syscall.S_IFDIR|0750})
	if got.Mode != syscall.S_IFDIR || got.Ino != inodeNumber(id) {
		t.Fatalf("stableAttr = %+v, want type=%#o ino=%d", got, syscall.S_IFDIR, inodeNumber(id))
	}
}

func TestFillAttrMapsOrchestrationAttributes(t *testing.T) {
	id := testObjectID(t)
	atime, mtime, ctime := time.Unix(123,456), time.Unix(234,567), time.Unix(345,678)
	attributes := orchestration.ObjectAttributes{ObjectID:id, Mode:syscall.S_IFREG|0640, UID:17, GID:23, Nlink:2, Size:1537, Atime:atime, Mtime:mtime, Ctime:ctime}
	var out gofuse.Attr
	fillAttr(&out, attributes)
	if out.Ino != inodeNumber(id) || out.Mode != attributes.Mode || out.Nlink != 2 || out.Size != 1537 {
		t.Fatalf("attribute identity/size mapping incorrect: %+v", out)
	}
	if out.Uid != 17 || out.Gid != 23 { t.Fatalf("owner mapping incorrect: uid=%d gid=%d", out.Uid,out.Gid) }
	if out.Atime != 123 || out.Atimensec != 456 || out.Mtime != 234 || out.Mtimensec != 567 || out.Ctime != 345 || out.Ctimensec != 678 {
		t.Fatalf("timestamp mapping incorrect: %+v", out)
	}
	if out.Blksize != 4096 || out.Blocks != 4 { t.Fatalf("block mapping incorrect: blksize=%d blocks=%d", out.Blksize,out.Blocks) }
}

func TestFillAttrBlockCountRoundsUp(t *testing.T) {
	for _, tc := range []struct{size, blocks uint64}{{0,0},{1,1},{512,1},{513,2}} {
		var out gofuse.Attr
		fillAttr(&out, orchestration.ObjectAttributes{Size:tc.size})
		if out.Blocks != tc.blocks { t.Errorf("size %d: Blocks=%d, want %d",tc.size,out.Blocks,tc.blocks) }
	}
}

type foreignHandle struct{}

func TestReadWriteRejectInvalidHandleAndOffset(t *testing.T) {
	n := &node{}
	for _, fh := range []interface{}{nil, &foreignHandle{}} {
		if _, errno := n.Read(nil, fh, make([]byte,1),0); errno != syscall.EBADF { t.Errorf("Read invalid handle errno=%v, want EBADF",errno) }
		if _, errno := n.Write(nil, fh, []byte("x"),0); errno != syscall.EBADF { t.Errorf("Write invalid handle errno=%v, want EBADF",errno) }
	}
	if _, errno := n.Read(nil,nil,make([]byte,1),-1); errno != syscall.EBADF { t.Errorf("Read negative offset errno=%v, want EBADF",errno) }
	if _, errno := n.Write(nil,nil,[]byte("x"),-1); errno != syscall.EBADF { t.Errorf("Write negative offset errno=%v, want EBADF",errno) }
}

func TestHandleOperationsRejectInvalidHandle(t *testing.T) {
	n:=&node{}
	if errno:=n.Flush(nil,nil); errno!=syscall.EBADF { t.Errorf("Flush errno=%v, want EBADF",errno) }
	if errno:=n.Fsync(nil,nil,0); errno!=syscall.EBADF { t.Errorf("Fsync errno=%v, want EBADF",errno) }
	if errno:=n.Release(nil,nil); errno!=syscall.EBADF { t.Errorf("Release errno=%v, want EBADF",errno) }
	fh:=&foreignHandle{}
	if errno:=n.Flush(nil,fh); errno!=syscall.EBADF { t.Errorf("Flush foreign handle errno=%v, want EBADF",errno) }
	if errno:=n.Fsync(nil,fh,0); errno!=syscall.EBADF { t.Errorf("Fsync foreign handle errno=%v, want EBADF",errno) }
	if errno:=n.Release(nil,fh); errno!=syscall.EBADF { t.Errorf("Release foreign handle errno=%v, want EBADF",errno) }
}

func TestNewRejectsNilOrchestration(t *testing.T) {
	if _,err:=New(nil,testObjectID(t)); err==nil { t.Fatal("New with nil Orchestration succeeded") }
}

func TestRenameRejectsUnsupportedFlagsBeforeBackendCall(t *testing.T) {
	if errno:=(&node{}).Rename(nil,"old",nil,"new",1); errno!=syscall.EINVAL { t.Fatalf("Rename flags errno=%v, want EINVAL",errno) }
}

func TestParseObjectID(t *testing.T) {
	if _,err:=ParseObjectID("not-a-uuid"); err==nil { t.Fatal("invalid UUID accepted") }
	got,err:=ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err!=nil || got!=testObjectID(t) { t.Fatalf("valid ParseObjectID = %v, %v",got,err) }
}

func TestCheckAccessOwnerGroupOtherAndRoot(t *testing.T) {
	attributes:=orchestration.ObjectAttributes{Mode:syscall.S_IFREG|0750,UID:1001,GID:2001}
	tests:=[]struct{name string; uid,gid,mask uint32; want syscall.Errno}{
		{"owner read/write",1001,9999,gofuse.R_OK|gofuse.W_OK,0},
		{"owner denied execute",1001,9999,gofuse.X_OK,syscall.EACCES},
		{"group execute",1002,2001,gofuse.X_OK,0},
		{"group denied write",1002,2001,gofuse.W_OK,syscall.EACCES},
		{"other denied read",1002,9999,gofuse.R_OK,syscall.EACCES},
		{"root read/write",0,0,gofuse.R_OK|gofuse.W_OK,0},
		{"root execute without execute bit",0,0,gofuse.X_OK,syscall.EACCES},
	}
	for _,tc:=range tests { t.Run(tc.name,func(t *testing.T){ if got:=checkAccess(attributes,tc.uid,tc.gid,tc.mask); got!=tc.want { t.Fatalf("checkAccess=%v, want %v",got,tc.want) } }) }
}
