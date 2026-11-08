//go:build !linux && !darwin

package inventory

import (
	"context"
	"errors"
	"runtime"

	"github.com/St1viG/cj/internal/agent/driver"
)

func discover(context.Context, Options) (driver.Inventory, error) {
	return driver.Inventory{}, errors.New("inventory: unsupported OS " + runtime.GOOS)
}
