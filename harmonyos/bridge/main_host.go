//go:build !openharmony

package main

import (
	"fmt"
	"os"
)

// main keeps repository-wide Go builds honest on non-OpenHarmony hosts. The
// actual bridge entry points are exported from main.go and must be compiled as
// a C shared library with the openharmony build tag.
func main() {
	fmt.Fprintln(os.Stderr, "the HarmonyOS bridge must be built with -tags openharmony -buildmode=c-shared")
	os.Exit(2)
}
