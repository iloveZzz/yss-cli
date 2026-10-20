//go:build windows

package mcp

import "io"

// os.File.Close cancels pending Windows pipe I/O via CancelIoEx.
func prepareOwnedInput(in io.ReadCloser) (io.ReadCloser, error) { return in, nil }
