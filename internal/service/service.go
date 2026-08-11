package service

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	serviceName        = "ecs"
	serviceDisplayName = "ECS distributed agent node"
	serviceDescription = "ECS local-first distributed agent runtime"
)

type manager interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Status() error
}

type options struct {
	executable string
	configPath string
	stdout     io.Writer
	stderr     io.Writer
}

// RunCLI manages the native background service for the current operating
// system. Administrative privileges are required by all three system managers.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	action := args[0]
	switch action {
	case "install", "uninstall", "start", "stop", "restart", "status":
	default:
		fmt.Fprintf(stderr, "unknown service action %q\n", action)
		printUsage(stderr)
		return 2
	}

	flags := flag.NewFlagSet("ecs service "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.json", "path to the JSON configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}

	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "resolve ECS executable: %v\n", err)
		return 1
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		fmt.Fprintf(stderr, "resolve ECS executable: %v\n", err)
		return 1
	}
	absoluteConfig, err := filepath.Abs(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve configuration path: %v\n", err)
		return 1
	}
	if action == "install" {
		if _, err := os.Stat(absoluteConfig); err != nil {
			fmt.Fprintf(stderr, "configuration %q: %v\n", absoluteConfig, err)
			return 1
		}
	}

	mgr, err := newManager(options{
		executable: executable,
		configPath: absoluteConfig,
		stdout:     stdout,
		stderr:     stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	run := func(action string) error {
		switch action {
		case "install":
			return mgr.Install()
		case "uninstall":
			return mgr.Uninstall()
		case "start":
			return mgr.Start()
		case "stop":
			return mgr.Stop()
		case "status":
			return mgr.Status()
		default:
			return errors.New("unsupported service action")
		}
	}
	if action == "restart" {
		_ = mgr.Stop()
		err = mgr.Start()
	} else {
		err = run(action)
	}
	if err != nil {
		fmt.Fprintf(stderr, "service %s: %v\n", action, err)
		return 1
	}
	if action != "status" {
		fmt.Fprintf(stdout, "ECS service %s completed\n", action)
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ecs service <install|uninstall|start|stop|restart|status> [-config path]")
}
