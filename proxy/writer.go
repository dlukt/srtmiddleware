package proxy

import (
	"io"
)

type Writer interface {
	io.WriteCloser
}
