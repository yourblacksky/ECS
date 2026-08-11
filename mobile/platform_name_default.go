//go:build !openharmony

package mobile

import "runtime"

func mobilePlatformName() string {
	return runtime.GOOS
}
