package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	binDir, err := localBinDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(binDir, "eve-mcp")
	if exe != dest {
		raw, err := os.ReadFile(exe)
		if err != nil {
			return err
		}
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
	}

	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(dest)
	case "linux":
		return installSystemd(dest)
	default:
		return fmt.Errorf("install is supported on macOS and Linux; on this OS just run %s", dest)
	}
}

func uninstallService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemd()
	default:
		return fmt.Errorf("nothing to uninstall on %s", runtime.GOOS)
	}
}

func localBinDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "bin"), nil
	}
	return "", fmt.Errorf("cannot resolve home directory")
}

func serviceWorkDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "eve-mcp")
	return dir, os.MkdirAll(dir, 0o700)
}

func installLaunchd(bin string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "eve-mcp.plist")
	logPath := filepath.Join(home, "Library", "Logs", "eve-mcp.log")
	workDir, err := serviceWorkDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	// WorkingDirectory is only so the process picks up ./.env; nothing is persisted there.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>eve-mcp</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, bin, workDir, logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	uid := strconv.Itoa(os.Getuid())
	domain := "gui/" + uid
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w\n%s", err, out)
	}
	_ = exec.Command("launchctl", "enable", domain+"/eve-mcp").Run()
	_ = exec.Command("launchctl", "kickstart", "-k", domain+"/eve-mcp").Run()
	fmt.Printf("installed %s\nlistening on http://127.0.0.1:8765/mcp\nlogs: %s\n", destOr(bin), logPath)
	return nil
}

func uninstallLaunchd() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "eve-mcp.plist")
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid, plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("removed the eve-mcp user service")
	return nil
}

func installSystemd(bin string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	unitPath := filepath.Join(unitDir, "eve-mcp.service")
	workDir, err := serviceWorkDir()
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=EVE Online MCP server
After=network-online.target

[Service]
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure

[Install]
WantedBy=default.target
`, bin, workDir)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w\n%s", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "eve-mcp.service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %w\n%s", err, out)
	}
	fmt.Printf("installed %s\nlistening on http://127.0.0.1:8765/mcp\n", destOr(bin))
	return nil
}

func uninstallSystemd() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", "eve-mcp.service").Run()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "eve-mcp.service")
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("removed the eve-mcp user service")
	return nil
}

func destOr(bin string) string { return bin }
