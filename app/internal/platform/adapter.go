package platform

import "context"

// Adapter represents one live-stream platform connection.
// Implementations must make Connect and Disconnect safe to call after a
// previous failed operation; credentials and platform-specific details stay
// inside the adapter.
type Adapter interface {
	ID() string
	Connect(context.Context) error
	Disconnect(context.Context) error
}
