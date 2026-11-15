package orchestration

import (
	"bytes"
	"syscall"
	"testing"
	"time"
)

func TestNamespaceEntryMutationsUpdateParentTimestamps(t *testing.T) {
	fixture := newNamespaceFixture(t)
	parentTimes := func() (time.Time, time.Time) {
		t.Helper()
		attr, err := fixture.layer.GetAttrLocked(GetAttrRequest{ObjectID: fixture.rootID})
		if err != nil {
			t.Fatal(err)
		}
		return attr.Attributes.Mtime, attr.Attributes.Ctime
	}
	assertUpdated := func(beforeMtime, beforeCtime time.Time) (time.Time, time.Time) {
		t.Helper()
		mtime, ctime := parentTimes()
		if !mtime.After(beforeMtime) || !ctime.After(beforeCtime) {
			t.Fatalf("parent timestamps = mtime %s ctime %s; want both after %s / %s", mtime, ctime, beforeMtime, beforeCtime)
		}
		if !mtime.Equal(ctime) {
			t.Fatalf("parent mtime %s and ctime %s differ", mtime, ctime)
		}
		return mtime, ctime
	}

	mtime, ctime := parentTimes()
	file, err := fixture.layer.CreateLocked(CreateRequest{ParentID: fixture.rootID, Name: "file"})
	if err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	_, err = fixture.layer.LinkLocked(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	_, err = fixture.layer.SymlinkLocked(SymlinkRequest{ParentID: fixture.rootID, Name: "link", Target: "target"})
	if err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	_, err = fixture.layer.MkdirLocked(MkdirRequest{ParentID: fixture.rootID, Name: "directory"})
	if err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	if _, err = fixture.layer.UnlinkLocked(UnlinkRequest{ParentID: fixture.rootID, Name: "link"}); err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)
	if _, err = fixture.layer.UnlinkLocked(UnlinkRequest{ParentID: fixture.rootID, Name: "alias"}); err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)
	if _, err = fixture.layer.RmdirLocked(RmdirRequest{ParentID: fixture.rootID, Name: "directory"}); err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	if _, err = fixture.layer.RenameLocked(RenameRequest{
		SourceParentID: fixture.rootID, SourceName: "file",
		DestinationParentID: fixture.rootID, DestinationName: "renamed",
	}); err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)

	dest, err := fixture.layer.MkdirLocked(MkdirRequest{ParentID: fixture.rootID, Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	mtime, ctime = assertUpdated(mtime, ctime)
	destinationBefore, err := fixture.layer.GetAttrLocked(GetAttrRequest{ObjectID: dest.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.layer.RenameLocked(RenameRequest{
		SourceParentID: fixture.rootID, SourceName: "renamed",
		DestinationParentID: dest.ObjectID, DestinationName: "moved",
	}); err != nil {
		t.Fatal(err)
	}
	assertUpdated(mtime, ctime)

	// A cross-parent move updates both affected directories.
	attr, err := fixture.layer.GetAttrLocked(GetAttrRequest{ObjectID: dest.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if !attr.Attributes.Mtime.After(destinationBefore.Attributes.Mtime) ||
		!attr.Attributes.Ctime.After(destinationBefore.Attributes.Ctime) ||
		!attr.Attributes.Mtime.Equal(attr.Attributes.Ctime) {
		t.Fatalf("destination timestamps = mtime %s ctime %s; before mtime %s ctime %s",
			attr.Attributes.Mtime, attr.Attributes.Ctime,
			destinationBefore.Attributes.Mtime, destinationBefore.Attributes.Ctime)
	}

}

func TestOpenFileRemainsReadableAfterUnlink(t *testing.T) {
	fixture := newNamespaceFixture(t)
	created, err := fixture.layer.CreateLocked(CreateRequest{ParentID: fixture.rootID, Name: "open-unlinked"})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.layer.OpenLocked(OpenRequest{ObjectID: created.ObjectID, Flags: uint32(syscall.O_RDWR)})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("Hello,_World!")
	if _, err := fixture.layer.WriteLocked(WriteRequest{Handle: opened.Handle, ObjectID: created.ObjectID, Offset: 0, Data: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.UnlinkLocked(UnlinkRequest{ParentID: fixture.rootID, Name: "open-unlinked"}); err != nil {
		t.Fatal(err)
	}

	read, err := fixture.layer.ReadLocked(ReadRequest{Handle: opened.Handle, ObjectID: created.ObjectID, Offset: 0, Size: uint32(len(payload))})
	if err != nil {
		t.Fatalf("Read after Unlink: %v", err)
	}
	if !bytes.Equal(read.Data, payload) {
		t.Fatalf("Read after Unlink = %q, want %q", read.Data, payload)
	}
	if _, err := fixture.layer.ReleaseLocked(ReleaseRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}
	if opened.Handle.runtimeFile.openCount != 0 {
		t.Fatalf("open count after Release = %d, want 0", opened.Handle.runtimeFile.openCount)
	}
}
