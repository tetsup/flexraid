package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/platform"
)

// SpinDownRequest identifies a configured drive by WWN.
type SpinDownRequest struct {
	Context context.Context
	WWN     string
}

// SpinDownResponse reports the physical drive that received the standby request.
type SpinDownResponse struct {
	WWN string
}

// SpinDown requests standby only for a drive registered during initialization.
func (layer *Management) SpinDown(request SpinDownRequest) (SpinDownResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SpinDownResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	result, err := platform.SpinDown(platform.SpinDownRequest{Context: request.Context, WWN: request.WWN})
	if err != nil {
		return SpinDownResponse{}, err
	}
	return SpinDownResponse{WWN: result.WWN}, nil
}
