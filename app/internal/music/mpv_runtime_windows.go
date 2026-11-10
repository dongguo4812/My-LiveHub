//go:build windows

package music

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveMpvExecutable never searches PATH. The release layout keeps the
// bundled runtime under runtime/mpv beside the application executable.
func resolveMpvExecutable(configured string) (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate My-LiveHub executable: %w", err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate My-LiveHub working directory: %w", err)
	}
	return resolveMpvExecutableFrom(configured, executablePath, workingDir)
}

func resolveMpvExecutableFrom(configured, executablePath, workingDir string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		path := strings.TrimSpace(configured)
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDir, path)
		}
		return validateMpvExecutable(path)
	}

	executableDir := filepath.Dir(executablePath)
	candidates := []string{
		filepath.Join(executableDir, "runtime", "mpv", "mpv.exe"),
		filepath.Join(workingDir, "runtime", "mpv", "mpv.exe"),
	}
	seen := make(map[string]struct{}, len(candidates))
	checked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		checked = append(checked, candidate)
		if resolved, err := validateMpvExecutable(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("未找到 My-LiveHub 随附的 mpv 运行库；请确认文件存在于 runtime/mpv/mpv.exe。已检查：%s", strings.Join(checked, "; "))
}

func validateMpvExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("mpv executable %q is unavailable: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("mpv executable %q is not a non-empty regular file", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve mpv executable path: %w", err)
	}
	return absolute, nil
}
