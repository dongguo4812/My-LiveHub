//go:build windows

package desktop

import (
	"os/exec"
	"syscall"
)

func launchUpdateHelper(scriptPath, taskPath string) error {
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Task", taskPath)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command.Start()
}
