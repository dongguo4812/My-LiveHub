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
	DefaultDir string
	LocalDir   string
}

// ResolvePaths calculates configuration locations without creating directories.
func ResolvePaths(env Environment, workingDir, userConfigDir string) (Paths, error) {
	switch env {
	case EnvironmentDevelopment:
		if workingDir == "" {
			return Paths{}, fmt.Errorf("development workspace directory is empty")
		}
		return Paths{
			DefaultDir: filepath.Join(workingDir, "config", "default"),
			LocalDir:   filepath.Join(workingDir, "config", "local"),
		}, nil
	case EnvironmentProduction:
		if userConfigDir == "" {
			return Paths{}, fmt.Errorf("user configuration directory is unavailable")
		}
		return Paths{
			LocalDir: filepath.Join(userConfigDir, "My-LiveHub", "config", "local"),
		}, nil
	default:
		return Paths{}, fmt.Errorf("unsupported environment %q", env)
	}
}

// ResolveRuntimePaths uses the workspace in development and the OS user config
// directory in production (%%APPDATA%% on Windows).
func ResolveRuntimePaths(env Environment) (Paths, error) {
	workingDir, err := os.Getwd()
	if err != nil && env == EnvironmentDevelopment {
		return Paths{}, fmt.Errorf("resolve development workspace: %w", err)
	}
	userConfigDir, err := os.UserConfigDir()
	if err != nil && env == EnvironmentProduction {
		return Paths{}, fmt.Errorf("resolve user configuration directory: %w", err)
	}
	return ResolvePaths(env, workingDir, userConfigDir)
}
