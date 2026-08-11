package service

import (
	"strings"
	"testing"
)

func TestSystemdUnitQuotesPathsAndPercent(t *testing.T) {
	unit := systemdUnit(`/opt/ECS node/ecs`, `/etc/ecs 100%/config.json`)
	for _, expected := range []string{
		`ExecStart="/opt/ECS node/ecs" -config "/etc/ecs 100%%/config.json"`,
		`WorkingDirectory="/etc/ecs 100%%"`,
		`Restart=on-failure`,
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit does not contain %q:\n%s", expected, unit)
		}
	}
}

func TestLaunchdPlistEscapesPaths(t *testing.T) {
	plist := launchdPlist(`/opt/ECS & node/ecs`, `/etc/ecs/config <prod>.json`)
	for _, expected := range []string{
		`<string>/opt/ECS &amp; node/ecs</string>`,
		`<string>/etc/ecs/config &lt;prod&gt;.json</string>`,
		`<key>KeepAlive</key>`,
	} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("plist does not contain %q:\n%s", expected, plist)
		}
	}
}
