package proxy

import (
	"fmt"
	"io"
	"net/url"

	srt "github.com/datarhei/gosrt"
)

func OpenReader(addr string, logger srt.Logger) (io.ReadCloser, error) {
	if len(addr) == 0 {
		return nil, fmt.Errorf("the address must not be empty")
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	if u.Scheme != "srt" {
		return nil, fmt.Errorf("only srt is supported")
	}

	config := srt.DefaultConfig()
	if err := config.UnmarshalQuery(u.RawQuery); err != nil {
		return nil, err
	}
	config.Logger = logger

	mode := u.Query().Get("mode")

	if mode == "" {
		mode = "caller"
	}

	switch mode {
	case "listener":
		ln, err := srt.Listen("srt", u.Host, config)
		if err != nil {
			return nil, err
		}

		conn, _, err := ln.Accept(func(req srt.ConnRequest) srt.ConnType {
			if config.StreamId != req.StreamId() {
				return srt.REJECT
			}

			req.SetPassphrase(config.Passphrase)

			return srt.PUBLISH
		})
		if err != nil {
			return nil, err
		}

		if conn == nil {
			return nil, fmt.Errorf("incoming connection rejected")
		}

		return conn, nil

	case "caller":
		conn, err := srt.Dial("srt", u.Host, config)
		if err != nil {
			return nil, err
		}

		return conn, nil

	default:
		return nil, fmt.Errorf("unsupported reader")
	}
}

func OpenWriter(addr string, logger srt.Logger) (io.WriteCloser, error) {
	if len(addr) == 0 {
		return nil, fmt.Errorf("the address must not be empty")
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	if u.Scheme != "srt" {
		return nil, fmt.Errorf("only srt is supported")
	}

	config := srt.DefaultConfig()
	if err := config.UnmarshalQuery(u.RawQuery); err != nil {
		return nil, err
	}
	config.Logger = logger

	mode := u.Query().Get("mode")

	if mode == "" {
		mode = "caller"
	}

	switch mode {
	case "listener":
		ln, err := srt.Listen("srt", u.Host, config)
		if err != nil {
			return nil, err
		}

		conn, _, err := ln.Accept(func(req srt.ConnRequest) srt.ConnType {
			if config.StreamId != req.StreamId() {
				return srt.REJECT
			}

			req.SetPassphrase(config.Passphrase)

			return srt.SUBSCRIBE
		})
		if err != nil {
			return nil, err
		}

		if conn == nil {
			return nil, fmt.Errorf("incoming connection rejected")
		}

		return conn, nil

	case "caller":
		conn, err := srt.Dial("srt", u.Host, config)
		if err != nil {
			return nil, err
		}

		return conn, nil
	default:
		return nil, fmt.Errorf("unsupported mode")
	}
}
