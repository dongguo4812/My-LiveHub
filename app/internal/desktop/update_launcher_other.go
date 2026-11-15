//go:build !windows

package desktop

import "os/exec"

func launchUpdateHelper(scriptPath, taskPath string) error {
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Task", taskPath)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
