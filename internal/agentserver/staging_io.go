package agentserver

import (
	"io"
	"net/http"
	"time"

	"durpdeploy/internal/httpstream"
)

type stagingIO struct {
	controller *http.ResponseController
	body       io.Reader
	output     http.ResponseWriter
	timeout    time.Duration
}

func newStagingIO(
	w http.ResponseWriter, r *http.Request, timeout time.Duration,
) (*stagingIO, error) {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &stagingIO{controller, r.Body, w, timeout}, nil
}

func (stream *stagingIO) Read(data []byte) (int, error) {
	if err := stream.controller.SetReadDeadline(
		time.Now().Add(stream.timeout),
	); err != nil {
		return 0, err
	}
	return stream.body.Read(data)
}

func (stream *stagingIO) Header() http.Header {
	return stream.output.Header()
}

func (stream *stagingIO) WriteHeader(status int) {
	stream.output.WriteHeader(status)
}

func (stream *stagingIO) Write(data []byte) (int, error) {
	if err := stream.controller.SetWriteDeadline(
		time.Now().Add(stream.timeout),
	); err != nil {
		return 0, err
	}
	n, err := stream.output.Write(data)
	if err != nil {
		return n, err
	}
	if err := httpstream.Flush(stream.output); err != nil {
		return n, err
	}
	return n, stream.controller.SetWriteDeadline(time.Time{})
}

func (stream *stagingIO) finish() error {
	return stream.controller.SetWriteDeadline(time.Now().Add(stream.timeout))
}
