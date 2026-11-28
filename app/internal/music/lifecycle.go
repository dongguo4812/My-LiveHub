package music

import "context"

type PlaybackEngineLifecycle interface {
	Start(context.Context) error
	Close() error
}

type PlaybackEngineValidator interface {
	Validate() error
}
