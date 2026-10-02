package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"durpdeploy/internal/artifact"
)

var ErrHTTP = errors.New("HTTP verification failed")

// HTTP checks allow private unicast services, but reject loopback, metadata
// addresses, redirects, environment proxies, and DNS rebinding like packages.
func CheckHTTP(ctx context.Context, settings Settings, output io.Writer) error {
	transport := artifact.NewTransport()
	transport.ResponseHeaderTimeout = time.Duration(
		settings.TimeoutSeconds,
	) * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(settings.TimeoutSeconds) * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		settings.Target, nil)
	if err != nil {
		return ErrHTTP
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors may include credentials in URL query parameters.
		return ErrHTTP
	}
	defer response.Body.Close()
	if _, err := fmt.Fprintf(output, "HTTP status: %d\n",
		response.StatusCode); err != nil {
		return fmt.Errorf("write verification status: %w", err)
	}
	const maxOutput = 64 * 1024
	if _, err := io.Copy(output, io.LimitReader(response.Body,
		maxOutput)); err != nil {
		return ErrHTTP
	}
	if _, err := io.WriteString(output, "\n"); err != nil {
		return fmt.Errorf("write verification output: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrHTTP
	}
	return nil
}
