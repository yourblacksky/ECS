package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ecs/internal/adminauth"
	"ecs/internal/config"
	"ecs/internal/core"
	"ecs/internal/platform"
	"ecs/internal/service"
	"ecs/internal/servicehost"
	"ecs/internal/toolbox"
	"ecs/version"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "tool" {
		os.Exit(toolbox.Run(os.Args[2], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) >= 2 && os.Args[1] == "service" {
		os.Exit(service.RunCLI(os.Args[2:], os.Stdout, os.Stderr))
	}
	var configPath string
	var showVersion bool
	flag.StringVar(&configPath, "config", "config.json", "path to the JSON configuration file")
	flag.BoolVar(&showVersion, "version", false, "print ECS version and build information")
	flag.Parse()
	if showVersion {
		fmt.Println(version.String())
		return
	}

	if servicehost.IsService() {
		if err := servicehost.Run("ECS", func(stop <-chan struct{}, reload <-chan struct{}) error {
			return run(configPath, stop, reload)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "run Windows service: %v\n", err)
			os.Exit(1)
		}
		return
	}

	stop := make(chan struct{})
	reload := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		defer close(stop)
		for sig := range signals {
			if sig == syscall.SIGHUP {
				select {
				case reload <- struct{}{}:
				default:
				}
				continue
			}
			return
		}
	}()
	if err := run(configPath, stop, reload); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(configPath string, stop <-chan struct{}, reload <-chan struct{}) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	localAdmin, err := adminauth.EnsureLocalToken(configPath, &cfg)
	if err != nil {
		return err
	}
	if localAdmin.Path != "" {
		state := "loaded"
		if localAdmin.Generated {
			state = "generated"
		}
		fmt.Fprintf(
			os.Stderr,
			"Admin authentication enabled: credential %s at %s (%s)\n",
			state,
			localAdmin.Path,
			localAdmin.Fingerprint,
		)
	}
	base := filepath.Dir(configPath)
	host := platform.NewDefaultHost(platform.RuntimePaths{
		Config: configPath,
		Data:   filepath.Join(base, "data"),
		Cache:  filepath.Join(base, "cache"),
	})
	node, err := core.New(cfg, host)
	if err != nil {
		return fmt.Errorf("initialize ECS: %w", err)
	}
	if err := node.Start(context.Background()); err != nil {
		return fmt.Errorf("start ECS: %w", err)
	}

	runtimeErrors := make(chan error, 1)
	go func() { runtimeErrors <- node.Wait() }()
	for {
		select {
		case <-reload:
			if err := node.Reload(); err != nil {
				fmt.Fprintf(os.Stderr, "reload ECS: %v\n", err)
			}
		case <-stop:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := node.Stop(ctx)
			cancel()
			if err != nil {
				return fmt.Errorf("stop ECS: %w", err)
			}
			return nil
		case err := <-runtimeErrors:
			if err != nil {
				return fmt.Errorf("ECS stopped: %w", err)
			}
			return nil
		}
	}
}
