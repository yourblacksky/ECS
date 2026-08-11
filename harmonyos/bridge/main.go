//go:build openharmony

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"ecs/mobile"
)

// runtimeAssets contains the same manifests, prompts, roles, pipelines, and
// Office stages used by the desktop and other mobile ECS nodes.
//
//go:embed generated/runtime
var runtimeAssets embed.FS

func materializeRuntime(dataDir string) error {
	if err := fs.WalkDir(runtimeAssets, "generated/runtime", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, "generated/runtime")
		relative = strings.TrimPrefix(relative, "/")
		if relative == "" {
			return nil
		}
		target := filepath.Join(dataDir, filepath.FromSlash(relative))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		// Preserve a user-edited local configuration across application starts.
		if relative == "config.json" {
			if _, err := os.Stat(target); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		content, err := runtimeAssets.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	}); err != nil {
		return err
	}
	if err := migrateHarmonyNetworkDefaults(dataDir); err != nil {
		return err
	}
	return migrateHarmonyLLMDefaults(dataDir)
}

func resultString(err error) *C.char {
	if err == nil {
		return C.CString("")
	}
	return C.CString(err.Error())
}

//export ECSHarmonyStart
func ECSHarmonyStart(dataDir *C.char, secretsJSON *C.char) *C.char {
	directory := C.GoString(dataDir)
	if err := materializeRuntime(directory); err != nil {
		encoded, _ := json.Marshal(map[string]any{"running": false, "error": err.Error()})
		return C.CString(string(encoded))
	}
	if err := mobile.StartWithSecrets("", directory, C.GoString(secretsJSON)); err != nil {
		encoded, _ := json.Marshal(map[string]any{"running": false, "error": err.Error()})
		return C.CString(string(encoded))
	}
	return C.CString(mobile.StatusJSON())
}

//export ECSHarmonyStop
func ECSHarmonyStop() *C.char {
	return resultString(mobile.Stop())
}

//export ECSHarmonyStatusJSON
func ECSHarmonyStatusJSON() *C.char {
	return C.CString(mobile.StatusJSON())
}

//export ECSHarmonySetLLMAPIKey
func ECSHarmonySetLLMAPIKey(value *C.char) *C.char {
	return resultString(mobile.SetLLMAPIKey(C.GoString(value)))
}

//export ECSHarmonyDiscoveryAnnouncement
func ECSHarmonyDiscoveryAnnouncement() *C.char {
	return C.CString(mobile.DiscoveryAnnouncement())
}

//export ECSHarmonyPushDiscoveryAnnouncement
func ECSHarmonyPushDiscoveryAnnouncement(payload *C.char) *C.char {
	return resultString(mobile.PushDiscoveryAnnouncement(C.GoString(payload)))
}

//export ECSHarmonyFree
func ECSHarmonyFree(value *C.char) {
	C.free(unsafe.Pointer(value))
}

func main() {}
