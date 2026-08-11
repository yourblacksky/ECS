//go:build !linux && !darwin && !windows

package service

import (
	"fmt"
	"runtime"
)

func newManager(options) (manager, error) {
	return nil, fmt.Errorf("native service management is not supported on %s", runtime.GOOS)
}
