package platform

import (
	"context"
	"errors"
	"testing"
)

func TestResolveDriveRequiresWWN(t *testing.T) {
	if _, err := ResolveDrive(DriveRequest{}); err == nil {
		t.Fatal("ResolveDrive succeeded without a WWN")
	}
}

func TestResolveDriveRejectsPathLikeWWN(t *testing.T) {
	for _, wwn := range []string{"../sda", "wwn/a", "*", "a?b", "[abc]"} {
		if _, err := ResolveDrive(DriveRequest{WWN: wwn}); err == nil {
			t.Errorf("ResolveDrive(%q) succeeded", wwn)
		}
	}
}

func TestResolveDriveHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveDrive(DriveRequest{Context: ctx, WWN: "0x5000000000000000"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveDrive error = %v, want context.Canceled", err)
	}
}

func TestSpinOperationsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SpinUp(SpinUpRequest{Context: ctx, WWN: "0x5000000000000000"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SpinUp error = %v, want context.Canceled", err)
	}
	if _, err := SpinDown(SpinDownRequest{Context: ctx, WWN: "0x5000000000000000"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SpinDown error = %v, want context.Canceled", err)
	}
}
