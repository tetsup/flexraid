package management

import (
	"context"
	"strings"
	"testing"
)

func TestValidateDriveConfig(t *testing.T) {
	drives := []DriveConfig{
		{Root: "/srv/d1/data", Mountpoint: "/srv/d1", WWN: "wwn-1"},
		{Root: "/srv/d2/data", Mountpoint: "/srv/d2", WWN: "wwn-2"},
	}
	got, err := validateDriveConfig(drives)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["wwn-1"].Mountpoint != "/srv/d1" {
		t.Fatalf("unexpected drive mapping: %+v", got)
	}
}

func TestValidateDriveConfigRejectsMissingPairMember(t *testing.T) {
	_, err := validateDriveConfig([]DriveConfig{{Root: "/srv/d1/data", WWN: "wwn-1"}})
	if err == nil || !strings.Contains(err.Error(), "requires root, mountpoint, and WWN") {
		t.Fatalf("error = %v, want missing mapping member", err)
	}
}

func TestValidateDriveConfigRejectsDuplicateFields(t *testing.T) {
	tests := []struct {
		name   string
		drives []DriveConfig
	}{
		{"root", []DriveConfig{{Root: "/r", Mountpoint: "/m1", WWN: "w1"}, {Root: "/r", Mountpoint: "/m2", WWN: "w2"}}},
		{"mountpoint", []DriveConfig{{Root: "/r1", Mountpoint: "/m", WWN: "w1"}, {Root: "/r2", Mountpoint: "/m", WWN: "w2"}}},
		{"WWN", []DriveConfig{{Root: "/r1", Mountpoint: "/m1", WWN: "w"}, {Root: "/r2", Mountpoint: "/m2", WWN: "w"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateDriveConfig(test.drives); err == nil {
				t.Fatal("validateDriveConfig succeeded with duplicate mapping")
			}
		})
	}
}

func TestSpinUpRejectsUnconfiguredWWN(t *testing.T) {
	layer := &Management{drives: map[string]DriveConfig{"configured": {WWN: "configured"}}}
	if _, err := layer.SpinUp(SpinUpRequest{WWN: "unknown"}); err == nil {
		t.Fatal("SpinUp succeeded for an unconfigured WWN")
	}
}

func TestSpinDownRejectsUnconfiguredWWN(t *testing.T) {
	layer := &Management{drives: map[string]DriveConfig{"configured": {WWN: "configured"}}}
	if _, err := layer.SpinDown(SpinDownRequest{WWN: "unknown"}); err == nil {
		t.Fatal("SpinDown succeeded for an unconfigured WWN")
	}
}

func TestDriveCleanupPermissionsAreIndependentAndMutable(t *testing.T) {
	layer := &Management{
		drives: map[string]DriveConfig{"wwn-1": {WWN: "wwn-1"}},
		cleanupPermissions: map[string]DriveCleanupPermissions{
			"wwn-1": {DeleteAllowed: false, PreserveAllowed: true},
		},
	}
	ctx := context.Background()

	if _, err := layer.SetDriveCleanupDeletePermission(SetDriveCleanupDeletePermissionRequest{
		Context: ctx, WWN: "wwn-1", Allowed: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := layer.GetDriveCleanupPermissions(GetDriveCleanupPermissionsRequest{Context: ctx, WWN: "wwn-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Permissions.DeleteAllowed || !got.Permissions.PreserveAllowed {
		t.Fatalf("permissions after delete update = %+v", got.Permissions)
	}

	if _, err := layer.SetDriveCleanupPreservePermission(SetDriveCleanupPreservePermissionRequest{
		Context: ctx, WWN: "wwn-1", Allowed: false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = layer.GetDriveCleanupPermissions(GetDriveCleanupPermissionsRequest{Context: ctx, WWN: "wwn-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Permissions.DeleteAllowed || got.Permissions.PreserveAllowed {
		t.Fatalf("permissions after preserve update = %+v", got.Permissions)
	}
}

func TestDriveCleanupPermissionRejectsUnknownWWN(t *testing.T) {
	layer := &Management{
		drives: map[string]DriveConfig{"wwn-1": {WWN: "wwn-1"}},
		cleanupPermissions: map[string]DriveCleanupPermissions{
			"wwn-1": {},
		},
	}
	_, err := layer.SetDriveCleanupDeletePermission(SetDriveCleanupDeletePermissionRequest{
		Context: context.Background(), WWN: "unknown", Allowed: true,
	})
	if err == nil {
		t.Fatal("permission update succeeded for unknown WWN")
	}
}

func TestDriveCleanupPermissionRejectsCanceledContext(t *testing.T) {
	layer := &Management{
		drives: map[string]DriveConfig{"wwn-1": {WWN: "wwn-1"}},
		cleanupPermissions: map[string]DriveCleanupPermissions{
			"wwn-1": {},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := layer.SetDriveCleanupDeletePermission(SetDriveCleanupDeletePermissionRequest{
		Context: ctx, WWN: "wwn-1", Allowed: true,
	})
	if err == nil {
		t.Fatal("permission update succeeded with canceled context")
	}
}
