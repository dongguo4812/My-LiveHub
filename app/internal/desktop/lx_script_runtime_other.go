//go:build !lxproduction || !cgo

package desktop

import "github.com/dongguo4812/My-LiveHub/app/internal/config"

func newLXScriptRuntime(_ *config.Manager) lxScriptRuntime { return nil }
