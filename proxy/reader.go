package proxy

import (
	"io"
)

type Reader interface {
	io.ReadCloser
}

type DebugReaderOptions struct {
	Bitrate uint64
}
