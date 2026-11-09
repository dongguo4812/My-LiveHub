package bilibili

import (
	"crypto/rand"
	"io"
)

type randReader struct{}
func (randReader) Read(p []byte) (int, error) { return io.ReadFull(rand.Reader, p) }
