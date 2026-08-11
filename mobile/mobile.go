// Package mobile exposes a gomobile-friendly facade over the ECS core.
// Keep this API limited to primitive values so the same package can generate
// Android AAR, Apple XCFramework, and HarmonyOS C bridge bindings.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ecs/internal/config"
	"ecs/internal/core"
	"ecs/internal/platform"
	"ecs/internal/protocol"
)

var singleton struct {
	sync.Mutex
	runtime            *core.Runtime
	discovery          *platform.BridgeDiscovery
	deviceCapabilities DeviceCapabilityProvider
}

// DeviceCapabilityProvider is implemented by Android and iOS hosts. It keeps
// native hardware and OS APIs outside the Go runtime while presenting one
// portable JSON contract to ECS.
type DeviceCapabilityProvider interface {
	// Availability returns an empty string when the capability is currently
	// usable, otherwise a stable reason such as permission_denied.
	Availability(name string) string
	Execute(name, argumentsJSON string) (string, error)
}

// SetDeviceCapabilityProvider installs the native bridge used by @device
// capabilities. Hosts should call it before Start.
func SetDeviceCapabilityProvider(provider DeviceCapabilityProvider) {
	singleton.Lock()
	defer singleton.Unlock()
	singleton.deviceCapabilities = provider
}

// ClearDeviceCapabilityProvider releases the native bridge after the runtime
// stops. Native hosts remain responsible for releasing their OS resources.
func ClearDeviceCapabilityProvider() {
	singleton.Lock()
	defer singleton.Unlock()
	singleton.deviceCapabilities = nil
}

// Start writes the supplied configuration into the app-private data directory
// and starts one ECS node. Passing an empty configJSON reuses config.json.
func Start(configJSON, dataDir string) error {
	return StartWithSecrets(configJSON, dataDir, "")
}

// StartWithSecrets starts ECS with an in-memory JSON object of secret-name to
// secret-value mappings. The secret values are never written to config.json.
func StartWithSecrets(configJSON, dataDir, secretsJSON string) error {
	singleton.Lock()
	defer singleton.Unlock()
	if singleton.runtime != nil {
		return errors.New("ECS mobile runtime is already started")
	}
	if dataDir == "" {
		return errors.New("dataDir is required")
	}
	absoluteDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absoluteDataDir, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(absoluteDataDir, "config.json")
	if configJSON != "" {
		if !json.Valid([]byte(configJSON)) {
			return errors.New("configJSON must contain one valid JSON document")
		}
		if err := writePrivateFile(configPath, []byte(configJSON)); err != nil {
			return err
		}
	}
	secretValues := map[string]string{}
	if secretsJSON != "" {
		if err := json.Unmarshal([]byte(secretsJSON), &secretValues); err != nil {
			return errors.New("secretsJSON must be an object of string values")
		}
	}
	secrets := platform.NewMemorySecrets(secretValues)
	cfg, err := config.LoadWithSecrets(configPath, secrets)
	if err != nil {
		return err
	}
	platformName := mobilePlatformName()
	discovery := platform.NewBridgeDiscovery(mobileDiscoveryName(platformName))
	toolExecutor := platform.ToolExecutor(platform.ProcessTools{Enabled: false})
	if singleton.deviceCapabilities != nil {
		toolExecutor = deviceCapabilityBridge{provider: singleton.deviceCapabilities}
	}
	host := platform.NewHost(platform.HostOptions{
		Paths: platform.RuntimePaths{
			Config: configPath,
			Data:   filepath.Join(absoluteDataDir, "data"),
			Cache:  filepath.Join(absoluteDataDir, "cache"),
		},
		Secrets:      secrets,
		Discovery:    discovery,
		Tools:        toolExecutor,
		Capabilities: mobileCapabilities(platformName),
	})
	nodeRuntime, err := core.New(cfg, host)
	if err != nil {
		return err
	}
	if err := nodeRuntime.Start(context.Background()); err != nil {
		return err
	}
	singleton.runtime = nodeRuntime
	singleton.discovery = discovery
	return nil
}

func mobileDiscoveryName(platformName string) string {
	switch platformName {
	case "android":
		return "android-nsd"
	case "ios":
		return "bonjour"
	case "harmonyos":
		return "harmony-discovery"
	default:
		return "platform-discovery"
	}
}

func mobileCapabilities(goos string) protocol.PlatformCapabilities {
	capabilities := protocol.PlatformCapabilities{
		Name:               goos,
		RuntimeMode:        "foreground",
		TopologyRole:       "peer",
		AcceptsChildren:    true,
		SupportsInbound:    true,
		SupportsBackground: false,
		SupportsSubprocess: false,
		SupportsMulticast:  goos == "android",
		SupportsPushWakeup: false,
	}
	if goos == "ios" {
		// iOS may accept requests while the app is active, but suspension makes
		// it unsuitable as an automatically selected recursive parent.
		capabilities.TopologyRole = "leaf"
		capabilities.AcceptsChildren = false
		capabilities.SupportsMulticast = false
	}
	if goos == "harmonyos" {
		// HarmonyOS owns mDNS/DNS-SD through NetworkKit and feeds the shared
		// discovery contract through BridgeDiscovery.
		capabilities.SupportsMulticast = true
	}
	return capabilities
}

func Stop() error {
	singleton.Lock()
	defer singleton.Unlock()
	if singleton.runtime == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := singleton.runtime.Stop(ctx); err != nil {
		return err
	}
	singleton.runtime = nil
	singleton.discovery = nil
	return nil
}

func Reload() error {
	singleton.Lock()
	defer singleton.Unlock()
	if singleton.runtime == nil {
		return errors.New("ECS mobile runtime is not started")
	}
	return singleton.runtime.Reload()
}

// SetLLMAPIKey updates the running node's provider credential in memory.
func SetLLMAPIKey(value string) error {
	singleton.Lock()
	defer singleton.Unlock()
	if singleton.runtime == nil {
		return errors.New("ECS mobile runtime is not started")
	}
	singleton.runtime.SetLLMAPIKey(value)
	return nil
}

func StatusJSON() string {
	singleton.Lock()
	defer singleton.Unlock()
	if singleton.runtime == nil {
		return `{"running":false}`
	}
	encoded, err := json.Marshal(singleton.runtime.Status())
	if err != nil {
		return `{"running":false,"error":"encode status failed"}`
	}
	return string(encoded)
}

// PushDiscoveryAnnouncement injects one compact ECS announcement produced by a
// native NSD/Bonjour/NetworkKit browser.
func PushDiscoveryAnnouncement(payload string) error {
	singleton.Lock()
	discovery := singleton.discovery
	singleton.Unlock()
	if discovery == nil {
		return errors.New("ECS mobile discovery bridge is not started")
	}
	if !json.Valid([]byte(payload)) {
		return errors.New("discovery payload must be valid JSON")
	}
	return discovery.Inject([]byte(payload))
}

// DiscoveryAnnouncement returns the newest compact announcement generated by
// ECS for hosts that need to publish native service metadata.
func DiscoveryAnnouncement() string {
	singleton.Lock()
	discovery := singleton.discovery
	singleton.Unlock()
	if discovery == nil {
		return ""
	}
	return string(discovery.Latest())
}

func writePrivateFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
