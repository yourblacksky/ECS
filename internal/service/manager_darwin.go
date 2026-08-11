//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
)

const (
	launchdLabel = "io.github.yourblacksky.ecs"
	launchdPath  = "/Library/LaunchDaemons/io.github.yourblacksky.ecs.plist"
)

type darwinManager struct {
	options
}

func newManager(opts options) (manager, error) {
	return &darwinManager{options: opts}, nil
}

func (m *darwinManager) Install() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("installation requires root; rerun with sudo")
	}
	_ = m.command("bootout", "system/"+launchdLabel)
	if err := os.WriteFile(launchdPath, []byte(launchdPlist(m.executable, m.configPath)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", launchdPath, err)
	}
	if err := os.Chmod(launchdPath, 0o644); err != nil {
		return fmt.Errorf("set permissions on %s: %w", launchdPath, err)
	}
	if err := m.command("enable", "system/"+launchdLabel); err != nil {
		return err
	}
	if err := m.command("bootstrap", "system", launchdPath); err != nil {
		return err
	}
	return m.command("kickstart", "-k", "system/"+launchdLabel)
}

func (m *darwinManager) Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstallation requires root; rerun with sudo")
	}
	_ = m.command("bootout", "system/"+launchdLabel)
	_ = m.command("disable", "system/"+launchdLabel)
	if err := os.Remove(launchdPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", launchdPath, err)
	}
	return nil
}

func (m *darwinManager) Start() error {
	if err := m.command("enable", "system/"+launchdLabel); err != nil {
		return err
	}
	if err := m.command("bootstrap", "system", launchdPath); err == nil {
		return nil
	}
	return m.command("kickstart", "-k", "system/"+launchdLabel)
}

func (m *darwinManager) Stop() error {
	if err := m.command("disable", "system/"+launchdLabel); err != nil {
		return err
	}
	return m.command("bootout", "system/"+launchdLabel)
}

func (m *darwinManager) Status() error {
	return m.command("print", "system/"+launchdLabel)
}

func (m *darwinManager) command(args ...string) error {
	command := exec.Command("launchctl", args...)
	command.Stdout = m.stdout
	command.Stderr = m.stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("launchctl %s: %w", args[0], err)
	}
	return nil
}
