package version

import (
	_ "embed"
	"runtime"
	"runtime/debug"
	"strings"
)

//go:embed VERSION
var currentText string

// Commit and BuildDate may be overridden with -ldflags for detached release
// builds. Normal go builds fall back to the VCS metadata embedded by Go.
var (
	Commit    string
	BuildDate string
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
}

func Current() string {
	return strings.TrimSpace(currentText)
}

func Read() Info {
	info := Info{Version: Current(), Commit: Commit, BuildDate: BuildDate, GoVersion: runtime.Version()}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = setting.Value
				}
			case "vcs.time":
				if info.BuildDate == "" {
					info.BuildDate = setting.Value
				}
			case "vcs.modified":
				info.Dirty = setting.Value == "true"
			}
		}
	}
	return info
}

func String() string {
	info := Read()
	value := "ecs v" + info.Version
	if info.Commit != "" {
		commit := info.Commit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		value += " commit=" + commit
	}
	if info.BuildDate != "" {
		value += " built=" + info.BuildDate
	}
	if info.Dirty {
		value += " dirty=true"
	}
	value += " go=" + info.GoVersion
	return value
}
