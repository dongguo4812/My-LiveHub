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


// ConnectionFailureReporter is implemented by adapters that can detect an
// unexpected end of an established connection. The callback must not be used
// for an intentional Disconnect.
type ConnectionFailureReporter interface {
	SetConnectionFailureHandler(func(error))
}
