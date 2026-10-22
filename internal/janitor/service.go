package janitor

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func ServiceDefinition(platform, exe, config, state string) (string, error) {
	for _, s := range []string{exe, config, state} {
		if !filepath.IsAbs(s) || strings.ContainsAny(s, "\n\r\x00") {
			return "", errors.New("service paths must be absolute and contain no line breaks")
		}
	}
	switch platform {
	case "darwin":
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>sh.janitor.agent</string>
<key>ProgramArguments</key><array><string>%s</string><string>--config</string><string>%s</string><string>--state</string><string>%s</string><string>daemon</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>30</integer>
<key>ProcessType</key><string>Background</string>
</dict></plist>
`, xmlText(exe), xmlText(config), xmlText(state)), nil
	case "linux":
		quote := func(s string) string {
			return strconv.Quote(strings.ReplaceAll(strings.ReplaceAll(s, "%", "%%"), "$", "$$"))
		}
		return fmt.Sprintf(`[Unit]
Description=Janitor workspace storage manager
After=default.target

[Service]
Type=simple
ExecStart=%s --config %s --state %s daemon
Restart=on-failure
RestartSec=30
UMask=0077
Nice=10

[Install]
WantedBy=default.target
`, quote(exe), quote(config), quote(state)), nil
	}
	return "", errors.New("services support macOS and Linux")
}
func servicePath() (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "sh.janitor.agent.plist"), nil
	}
	if runtime.GOOS == "linux" {
		return filepath.Join(home, ".config", "systemd", "user", "janitor.service"), nil
	}
	return "", errors.New("unsupported OS")
}
func InstallService(config, state string) (string, error) {
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return "", e
	}
	config, e = filepath.Abs(config)
	if e != nil {
		return "", e
	}
	state, e = filepath.Abs(state)
	if e != nil {
		return "", e
	}
	body, e := ServiceDefinition(runtime.GOOS, exe, config, state)
	if e != nil {
		return "", e
	}
	path, e := servicePath()
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", fmt.Errorf("service exists or cannot be created; remove before reinstalling: %w", e)
	}
	_, e = f.WriteString(body)
	closeErr := f.Close()
	if e != nil {
		return path, e
	}
	if closeErr != nil {
		return path, closeErr
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path)
	} else {
		if out, e := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); e != nil {
			return path, fmt.Errorf("unit saved; daemon-reload failed: %s: %w", out, e)
		}
		cmd = exec.Command("systemctl", "--user", "enable", "--now", "janitor.service")
	}
	if out, e := cmd.CombinedOutput(); e != nil {
		return path, fmt.Errorf("service saved but could not start: %s: %w", out, e)
	}
	return path, nil
}
func UninstallService() (string, error) {
	path, e := servicePath()
	if e != nil {
		return "", e
	}
	if _, e = os.Stat(path); e != nil {
		return path, e
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), path)
	} else {
		cmd = exec.Command("systemctl", "--user", "disable", "--now", "janitor.service")
	}
	if out, e := cmd.CombinedOutput(); e != nil {
		return path, fmt.Errorf("could not stop service; definition retained: %s: %w", out, e)
	}
	if e = os.Remove(path); e != nil {
		return path, e
	}
	if runtime.GOOS == "linux" {
		if out, e := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); e != nil {
			return path, fmt.Errorf("removed service but reload failed: %s: %w", out, e)
		}
	}
	return path, nil
}
