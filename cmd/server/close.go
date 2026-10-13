package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
)

func closeOnExit(resource io.Closer) {
	if err := resource.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Warn("close server resource", "err", err)
	}
}
