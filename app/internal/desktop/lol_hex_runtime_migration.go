package desktop

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// MigrateLOLHexRuntimeData copies user-owned data from the legacy extension
// location into the built-in module location. Existing destination files win,
// so upgrades never overwrite newer local data with an older copy.
func MigrateLOLHexRuntimeData(legacyRoot, runtimeRoot string) error {
	if legacyRoot == "" || runtimeRoot == "" {
		return errors.New("LOL Hex 旧目录和内置运行目录不能为空")
	}
	legacy, err := filepath.Abs(legacyRoot)
	if err != nil {
		return fmt.Errorf("resolve legacy LOL Hex directory: %w", err)
	}
	runtime, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return fmt.Errorf("resolve built-in LOL Hex directory: %w", err)
	}
	if filepath.Clean(legacy) == filepath.Clean(runtime) {
		return nil
	}

	// Keep both source-mode and PyInstaller one-directory runtime data. The
	// nested paths matter because frozen Python resolves DATA/LOG_DIR beside
	// its executable while screenshot_dir may resolve relative to the worker cwd.
	for _, relative := range []string{
		"data",
		"screenshots",
		"logs",
		"dist/lol_hex_display/data",
		"dist/lol_hex_display/screenshots",
		"dist/lol_hex_display/logs",
	} {
		source := filepath.Join(legacy, relative)
		destination := filepath.Join(runtime, relative)
		if err := copyMissingLOLHexTree(source, destination); err != nil {
			return fmt.Errorf("migrate LOL Hex %s: %w", relative, err)
		}
	}
	return nil
}

func copyMissingLOLHexTree(source, destination string) error {
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", source)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			// Runtime data should not contain links; ignore them rather than
			// following a path that could escape either application directory.
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileInfo.Mode().Perm())
		if errors.Is(err, os.ErrExist) {
			_ = input.Close()
			return nil
		}
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil {
			_ = os.Remove(target)
			return copyErr
		}
		if inputCloseErr != nil {
			_ = os.Remove(target)
			return inputCloseErr
		}
		if outputCloseErr != nil {
			_ = os.Remove(target)
			return outputCloseErr
		}
		return nil
	})
}
