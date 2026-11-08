package orchestration

import (
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

func TestCountReachableObjectsCountsHardLinkTargetOnce(t *testing.T) {
	rootID, err := shared.NewObjectID()
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := shared.NewObjectID()
	if err != nil {
		t.Fatal(err)
	}
	linkID, err := shared.NewObjectID()
	if err != nil {
		t.Fatal(err)
	}
	root := &runtimeObject{header: header{ObjectID: rootID, Kind: objectKindDirectory}, children: make(map[string]*runtimeObject)}
	file := &runtimeObject{header: header{ObjectID: fileID, Kind: objectKindFile, ParentID: &rootID}}
	link := &runtimeObject{header: header{ObjectID: linkID, Kind: objectKindHardLink, ParentID: &rootID, TargetID: &fileID}}
	root.children["file"] = file
	root.children["link"] = link
	layer := &Orchestration{root: root, objects: map[shared.ObjectID]*runtimeObject{rootID: root, fileID: file, linkID: link}}

	if got := layer.countReachableObjects(); got != 2 {
		t.Fatalf("countReachableObjects() = %d, want 2", got)
	}
}
