//go:build tools

package mobile

// Keep gomobile's bind package in the module graph for reproducible AAR builds.
import _ "golang.org/x/mobile/bind"
