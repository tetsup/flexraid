package management

import (
	"fmt"

	physicalio "github.com/tetsup/flexraid/io"
)

// SetIOs publishes the per-drive I/O layers created during initialization.
func (layer *Management) SetIOs(ios []*physicalio.IO) error {
	if len(ios) != len(layer.driveOrder) {
		return fmt.Errorf("I/O layer count %d does not match configured drive count %d", len(ios), len(layer.driveOrder))
	}
	for index, device := range ios {
		if device == nil {
			return fmt.Errorf("I/O layer %d is nil", index)
		}
	}
	layer.mu.Lock()
	defer layer.mu.Unlock()
	if layer.Phase() != LifecycleInitializing {
		return fmt.Errorf("I/O layers can only be set during initialization")
	}
	if len(layer.ios) != 0 {
		return fmt.Errorf("I/O layers are already set")
	}
	layer.ios = append([]*physicalio.IO(nil), ios...)
	return nil
}
