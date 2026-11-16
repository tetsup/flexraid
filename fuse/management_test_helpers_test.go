package fuse

import (
	"testing"

	"github.com/tetsup/flexraid/management"
	"github.com/tetsup/flexraid/orchestration"
)

func newManagementForTest(t *testing.T, layer *orchestration.Orchestration) *management.Management {
	t.Helper()
	managed, err := management.New(management.NewRequest{
		Orchestration: layer,
		Config: management.Config{Disks: []management.DriveConfig{
			{Root: "/test/disk1/data", Mountpoint: "/test/disk1", WWN: "test-wwn-1"},
			{Root: "/test/disk2/data", Mountpoint: "/test/disk2", WWN: "test-wwn-2"},
			{Root: "/test/disk3/data", Mountpoint: "/test/disk3", WWN: "test-wwn-3"},
			{Root: "/test/disk4/data", Mountpoint: "/test/disk4", WWN: "test-wwn-4"},
		}},
	})
	if err != nil {
		t.Fatalf("initialize Management: %v", err)
	}
	return managed
}
