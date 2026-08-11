//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
)

type windowsManager struct {
	options
}

func newManager(opts options) (manager, error) {
	return &windowsManager{options: opts}, nil
}

func (m *windowsManager) Install() error {
	binaryPath := windowsQuote(m.executable) + " -config " + windowsQuote(m.configPath)
	if exec.Command("sc.exe", "query", "ECS").Run() == nil {
		if err := m.command("config", "ECS", "binPath=", binaryPath, "start=", "auto",
			"DisplayName=", serviceDisplayName); err != nil {
			return err
		}
	} else {
		if err := m.command("create", "ECS", "binPath=", binaryPath, "start=", "auto",
			"DisplayName=", serviceDisplayName); err != nil {
			return err
		}
	}
	if err := m.command("description", "ECS", serviceDescription); err != nil {
		return err
	}
	if err := m.command("failure", "ECS", "reset=", "86400", "actions=", "restart/5000/restart/15000/restart/30000"); err != nil {
		return err
	}
	return m.Start()
}

func (m *windowsManager) Uninstall() error {
	_ = m.Stop()
	return m.command("delete", "ECS")
}

func (m *windowsManager) Start() error  { return m.command("start", "ECS") }
func (m *windowsManager) Stop() error   { return m.command("stop", "ECS") }
func (m *windowsManager) Status() error { return m.command("query", "ECS") }

func (m *windowsManager) command(args ...string) error {
	command := exec.Command("sc.exe", args...)
	command.Stdout = m.stdout
	command.Stderr = m.stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("sc.exe %s: %w", args[0], err)
	}
	return nil
}

func windowsQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
