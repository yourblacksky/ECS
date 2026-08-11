//go:build darwin

package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type darwinMDNSPublisher struct {
	ctx         context.Context
	instance    string
	serviceType string
	domain      string
	port        int
	text        []string
	process     *darwinMDNSProcess
}

type darwinMDNSProcess struct {
	command *exec.Cmd
	done    chan struct{}
	once    sync.Once
}

func registerMDNSPublisher(
	ctx context.Context,
	instance string,
	serviceType string,
	domain string,
	port int,
	text []string,
) (mdnsPublisher, error) {
	publisher := &darwinMDNSPublisher{
		ctx:         ctx,
		instance:    instance,
		serviceType: serviceType,
		domain:      strings.Trim(domain, "."),
		port:        port,
	}
	if err := publisher.start(text); err != nil {
		return nil, err
	}
	return publisher, nil
}

func (p *darwinMDNSPublisher) SetText(text []string) error {
	if slices.Equal(p.text, text) && p.process != nil && p.process.running() {
		return nil
	}
	if p.process != nil {
		p.process.stop()
		p.process = nil
	}
	return p.start(text)
}

func (p *darwinMDNSPublisher) Shutdown() {
	if p.process != nil {
		p.process.stop()
		p.process = nil
	}
}

func (p *darwinMDNSPublisher) start(text []string) error {
	executable, err := exec.LookPath("dns-sd")
	if err != nil {
		return errors.New("macOS Bonjour publisher dns-sd is unavailable")
	}
	args := []string{
		"-R",
		p.instance,
		p.serviceType,
		p.domain,
		strconv.Itoa(p.port),
	}
	args = append(args, text...)
	command := exec.CommandContext(p.ctx, executable, args...)
	if err := command.Start(); err != nil {
		return err
	}
	process := &darwinMDNSProcess{
		command: command,
		done:    make(chan struct{}),
	}
	go func() {
		_ = command.Wait()
		close(process.done)
	}()
	p.process = process
	p.text = append(p.text[:0], text...)
	return nil
}

func (p *darwinMDNSProcess) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *darwinMDNSProcess) stop() {
	p.once.Do(func() {
		if p.command.Process == nil {
			return
		}
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = p.command.Process.Kill()
			<-p.done
		}
	})
}
