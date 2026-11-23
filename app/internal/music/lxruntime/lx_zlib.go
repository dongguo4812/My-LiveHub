//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

const lxMaxZlibInputBytes = 8 << 20
const lxMaxZlibOutputBytes = 8 << 20

func lxZlibDeflate(input []byte) ([]byte, error) {
	if len(input) > lxMaxZlibInputBytes {
		return nil, fmt.Errorf("zlib input exceeds %d bytes", lxMaxZlibInputBytes)
	}
	var output bytes.Buffer
	writer := zlib.NewWriter(&output)
	if _, err := writer.Write(input); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("zlib deflate: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("zlib deflate: %w", err)
	}
	if output.Len() > lxMaxZlibOutputBytes {
		return nil, fmt.Errorf("zlib output exceeds %d bytes", lxMaxZlibOutputBytes)
	}
	return output.Bytes(), nil
}

func lxZlibInflate(input []byte) ([]byte, error) {
	if len(input) > lxMaxZlibInputBytes {
		return nil, fmt.Errorf("zlib input exceeds %d bytes", lxMaxZlibInputBytes)
	}
	reader, err := zlib.NewReader(bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("zlib inflate: %w", err)
	}
	defer reader.Close()
	output, err := io.ReadAll(io.LimitReader(reader, lxMaxZlibOutputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("zlib inflate: %w", err)
	}
	if len(output) > lxMaxZlibOutputBytes {
		return nil, fmt.Errorf("zlib output exceeds %d bytes", lxMaxZlibOutputBytes)
	}
	return output, nil
}
