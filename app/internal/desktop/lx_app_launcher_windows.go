//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// launchLXApp resolves the executable registered for the lxmusic protocol and
// starts it without relying on a root URI being accepted by the deeplink router.
func launchLXApp() error {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, `lxmusic\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("未找到洛雪音乐的 Windows 协议注册信息：%w", err)
	}
	defer key.Close()

	command, _, err := key.GetStringValue("")
	if err != nil {
		return fmt.Errorf("读取洛雪音乐启动命令失败：%w", err)
	}
	executable := lxExecutableFromProtocolCommand(command)
	if executable == "" {
		return errors.New("洛雪音乐协议注册项中没有有效的 exe 路径")
	}
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		if err == nil {
			err = errors.New("路径指向目录")
		}
		return fmt.Errorf("洛雪音乐程序不存在：%s：%w", executable, err)
	}

	process := exec.Command(executable)
	if err := process.Start(); err != nil {
		return fmt.Errorf("启动洛雪音乐程序失败：%w", err)
	}
	return process.Process.Release()
}

// lxExecutableFromProtocolCommand extracts the executable from the command
// registered by Electron's setAsDefaultProtocolClient.
func lxExecutableFromProtocolCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.HasPrefix(command, `"`) {
		rest := command[1:]
		end := strings.Index(rest, `"`)
		if end <= 0 {
			return ""
		}
		return rest[:end]
	}
	lower := strings.ToLower(command)
	if end := strings.Index(lower, ".exe"); end >= 0 {
		return command[:end+4]
	}
	return ""
}
