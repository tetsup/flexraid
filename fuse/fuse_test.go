package fuse

import (
	"syscall"
	"testing"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fuse"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

func TestInodeNumberIsStableAndNonzero(t *testing.T) {
	id, err := shared.ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err != nil {
		t.Fatal(err)
	}
	if first, second := inodeNumber(id), inodeNumber(id); first == 0 || first != second {
		t.Fatalf("inodeNumber is not stable and nonzero: first=%d second=%d", first, second)
	}
}

func TestFillAttrMapsOrchestrationAttributes(t *testing.T) {
	id, err := shared.ParseObjectID("9b2b0d5a-4c7d-4f5b-8f44-4a81a2c4b6d1")
	if err != nil {
		t.Fatal(err)
	}
	atime := time.Unix(123, 456)
	mtime := time.Unix(234, 567)
	ctime := time.Unix(345, 678)
	attributes := orchestration.ObjectAttributes{
		ObjectID: id, Mode: syscall.S_IFREG | 0640, UID: 17, GID: 23,
		Nlink: 2, Size: 1537, Atime: atime, Mtime: mtime, Ctime: ctime,
	}
	var out gofuse.Attr
	fillAttr(&out, attributes)

	if out.Ino != inodeNumber(id) || out.Mode != attributes.Mode || out.Nlink != 2 || out.Size != 1537 {
		t.Fatalf("attribute identity/size mapping incorrect: %+v", out)
	}
	if out.Uid != 17 || out.Gid != 23 {
		t.Fatalf("owner mapping incorrect: uid=%d gid=%d", out.Uid, out.Gid)
	}
	if out.Atime != 123 || out.Atimensec != 456 ||
		out.Mtime != 234 || out.Mtimensec != 567 ||
		out.Ctime != 345 || out.Ctimensec != 678 {
		t.Fatalf("timestamp mapping incorrect: %+v", out)
	}
	if out.Blksize != 4096 || out.Blocks != 4 {
		t.Fatalf("block mapping incorrect: blksize=%d blocks=%d", out.Blksize, out.Blocks)
	}
}

func TestReadRejectsMissingFileHandle(t *testing.T) {
	n := &node{}
	if _, errno := n.Read(nil, nil, make([]byte, 1), 0); errno != syscall.EBADF {
		t.Fatalf("Read without handle errno = %v, want EBADF", errno)
	}
	if _, errno := n.Write(nil, nil, []byte("x"), 0); errno != syscall.EBADF {
		t.Fatalf("Write without handle errno = %v, want EBADF", errno)
	}
}
