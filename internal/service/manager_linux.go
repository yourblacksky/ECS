//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
)

const systemdUnitPath = "/etc/systemd/system/ecs.service"

type linuxManager struct {
	options
}

func newManager(opts options) (manager, error) {
	return &linuxManager{options: opts}, nil
}

func (m *linuxManager) Install() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("installation requires root; rerun with sudo")
	}
	if err := os.WriteFile(systemdUnitPath, []byte(systemdUnit(m.executable, m.configPath)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", systemdUnitPath, err)
	}
	if err := os.Chmod(systemdUnitPath, 0o644); err != nil {
		return fmt.Errorf("set permissions on %s: %w", systemdUnitPath, err)
	}
	if err := m.command("daemon-reload"); err != nil {
		return err
	}
	if err := m.command("enable", serviceName+".service"); err != nil {
		return err
	}
	return m.command("restart", serviceName+".service")
}

func (m *linuxManager) Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstallation requires root; rerun with sudo")
	}
	_ = m.command("disable", "--now", serviceName+".service")
	if err := os.Remove(systemdUnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", systemdUnitPath, err)
	}
	return m.command("daemon-reload")
}

func (m *linuxManager) Start() error { return m.command("start", serviceName+".service") }
func (m *linuxManager) Stop() error  { return m.command("stop", serviceName+".service") }
func (m *linuxManager) Status() error {
	return m.command("status", "--no-pager", serviceName+".service")
}

func (m *linuxManager) command(args ...string) error {
	command := exec.Command("systemctl", args...)
	command.Stdout = m.stdout
	command.Stderr = m.stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", args[0], err)
	}
	return nil
}
