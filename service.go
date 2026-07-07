package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const launchdLabel = "com.ts.ssh-tunnel"

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func serviceInstalled() bool {
	_, err := os.Stat(launchdPlistPath())
	return err == nil
}

const plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary}}</string>
    <string>--serve</string>
    <string>--port</string>
    <string>{{.Port}}</string>
    <string>--data-dir</string>
    <string>{{.DataDir}}</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>WorkingDirectory</key><string>{{.WorkDir}}</string>
  <key>StandardOutPath</key><string>{{.LogPath}}</string>
  <key>StandardErrorPath</key><string>{{.LogPath}}</string>
</dict>
</plist>
`

// installService 生成 launchd plist 并加载，使隧道服务开机自启、崩溃重启。
func installService(port int, dataDir string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	bin, _ = filepath.Abs(bin)
	workDir, _ := os.Getwd()
	logPath := filepath.Join(dataDir, "service.log")

	data := map[string]string{
		"Label":   launchdLabel,
		"Binary":  bin,
		"Port":    fmt.Sprintf("%d", port),
		"DataDir": dataDir,
		"WorkDir": workDir,
		"LogPath": logPath,
	}
	tmpl, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}

	plistPath := launchdPlistPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, buf.Bytes(), 0o644); err != nil {
		return err
	}
	// 先卸载可能存在的旧实例，再加载新的
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load 失败: %w", err)
	}
	return nil
}

// uninstallService 卸载 launchd 服务并删除 plist。
func uninstallService() error {
	plistPath := launchdPlistPath()
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
