//go:build !windows

package desktop

import "os/exec"

func launchUpdateHelper(scriptPath, taskPath string) error {
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Task", taskPath)
	return command.Start()
}
