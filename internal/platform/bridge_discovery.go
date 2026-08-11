package platform

import (
	"context"
	"errors"
	"sync"
)

// BridgeDiscovery accepts announcements from an OS-native discovery service
// such as Android NSD, Apple Bonjour, or HarmonyOS NetworkKit mDNS.
type BridgeDiscovery struct {
	name string

	mu      sync.RWMutex
	ctx     context.Context
	handler AnnouncementHandler
	latest  []byte
}

func NewBridgeDiscovery(name string) *BridgeDiscovery {
	return &BridgeDiscovery{name: name}
}

func (d *BridgeDiscovery) Name() string {
	if d.name == "" {
		return "platform-bridge"
	}
	return d.name
}

func (d *BridgeDiscovery) Start(
	ctx context.Context,
	_ string,
	handler AnnouncementHandler,
) error {
	if handler == nil {
		return errors.New("discovery announcement handler is required")
	}
	d.mu.Lock()
	d.ctx = ctx
	d.handler = handler
	d.mu.Unlock()
	return nil
}

// Announce retains the newest ECS payload so a native host may publish selected
// fields through its own service-discovery API.
func (d *BridgeDiscovery) Announce(_ context.Context, _ string, payload []byte) error {
	d.mu.Lock()
	d.latest = append(d.latest[:0], payload...)
	d.mu.Unlock()
	return nil
}

func (d *BridgeDiscovery) Inject(payload []byte) error {
	d.mu.RLock()
	ctx := d.ctx
	handler := d.handler
	d.mu.RUnlock()
	if handler == nil {
		return errors.New("discovery bridge is not started")
	}
	if ctx == nil || ctx.Err() != nil {
		return errors.New("discovery bridge is stopped")
	}
	handler(ctx, append([]byte(nil), payload...))
	return nil
}

func (d *BridgeDiscovery) Latest() []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]byte(nil), d.latest...)
}
