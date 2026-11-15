package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentProduction  Environment = "production"
)

// ResolveEnvironment treats only an explicit "dev" argument as development.
// All normal launches, including a double-clicked EXE, use production mode.
func ResolveEnvironment(args []string) Environment {
	for _, arg := range args {
		if arg == "dev" {
			return EnvironmentDevelopment
		}
	}
	return EnvironmentProduction
}

type Paths struct {
	DataDir    string
	DefaultDir string
	LocalDir   string
}

// ResolvePaths calculates configuration locations without creating directories.
func ResolvePaths(env Environment, workingDir, applicationDir string) (Paths, error) {
	switch env {
	case EnvironmentDevelopment:
		if workingDir == "" {
			return Paths{}, fmt.Errorf("development workspace directory is empty")
		}
		return Paths{
			DataDir:    workingDir,
			DefaultDir: filepath.Join(workingDir, "config", "default"),
			LocalDir:   filepath.Join(workingDir, "config", "local"),
		}, nil
	case EnvironmentProduction:
		if applicationDir == "" {
			return Paths{}, fmt.Errorf("application directory is unavailable")
		}
		return Paths{
			DataDir:  applicationDir,
			LocalDir: filepath.Join(applicationDir, "config", "local"),
		}, nil
	default:
		return Paths{}, fmt.Errorf("unsupported environment %q", env)
	}
}

// ResolveRuntimePaths keeps writable runtime data beside the application
// executable in production, and inside the current workspace in development.
func ResolveRuntimePaths(env Environment) (Paths, error) {
	workingDir, err := os.Getwd()
	if err != nil && env == EnvironmentDevelopment {
		return Paths{}, fmt.Errorf("resolve development workspace: %w", err)
	}
	applicationDir := ""
	if env == EnvironmentProduction {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return Paths{}, fmt.Errorf("resolve application executable: %w", executableErr)
		}
		applicationDir = filepath.Dir(executable)
	}
	return ResolvePaths(env, workingDir, applicationDir)
}
