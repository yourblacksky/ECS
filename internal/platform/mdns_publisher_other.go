//go:build !darwin

package platform

import (
	"context"

	"github.com/grandcat/zeroconf"
)

type zeroconfMDNSPublisher struct {
	server *zeroconf.Server
}

func registerMDNSPublisher(
	_ context.Context,
	instance string,
	serviceType string,
	_ string,
	port int,
	text []string,
) (mdnsPublisher, error) {
	host, addresses, interfaces, err := mdnsProxyIdentity()
	if err != nil {
		return nil, err
	}
	server, err := zeroconf.RegisterProxy(
		instance,
		serviceType,
		mdnsRegistrationDomain(),
		port,
		host,
		addresses,
		text,
		interfaces,
	)
	if err != nil {
		return nil, err
	}
	return &zeroconfMDNSPublisher{server: server}, nil
}

func (p *zeroconfMDNSPublisher) SetText(text []string) error {
	p.server.SetText(text)
	return nil
}

func (p *zeroconfMDNSPublisher) Shutdown() {
	p.server.Shutdown()
}
