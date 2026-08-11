package service

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
)

func systemdUnit(executable, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s -config %s
WorkingDirectory=%s
Restart=on-failure
RestartSec=5s
KillSignal=SIGTERM
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, serviceDisplayName, systemdQuote(executable), systemdQuote(configPath),
		systemdQuote(filepath.Dir(configPath)))
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `%`, `%%`)
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func launchdPlist(executable, configPath string) string {
	escape := func(value string) string {
		var builder strings.Builder
		_ = xml.EscapeText(&builder, []byte(value))
		return builder.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.github.yourblacksky.ecs</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-config</string>
    <string>%s</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>/var/log/ecs.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/ecs.error.log</string>
</dict>
</plist>
`, escape(executable), escape(configPath), escape(filepath.Dir(configPath)))
}
