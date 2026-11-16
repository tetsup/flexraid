package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/platform"
)

// SpinUpRequest identifies a configured drive by WWN.
type SpinUpRequest struct {
	Context context.Context
	WWN     string
}

// SpinUpResponse reports the physical drive that was woken.
type SpinUpResponse struct {
	WWN string
}

// SpinUp requests spin-up only for a drive registered during initialization.
func (layer *Management) SpinUp(request SpinUpRequest) (SpinUpResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if _, exists := layer.drives[request.WWN]; !exists {
		return SpinUpResponse{}, fmt.Errorf("WWN %q is not configured", request.WWN)
	}
	result, err := platform.SpinUp(platform.SpinUpRequest{Context: request.Context, WWN: request.WWN})
	if err != nil {
		return SpinUpResponse{}, err
	}
	return SpinUpResponse{WWN: result.WWN}, nil
}
