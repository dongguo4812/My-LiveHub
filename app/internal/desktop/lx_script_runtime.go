package desktop

import (
	"context"

)

type lxScriptRuntime interface {
	Available() bool
	Start(context.Context) error
	ResolvePlaybackURL(context.Context, string, any) (string, error)
	Close() error
}

