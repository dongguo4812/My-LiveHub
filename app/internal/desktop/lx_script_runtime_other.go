//go:build !lxproduction || !cgo

package desktop

func newLXScriptRuntime() lxScriptRuntime { return nil }
