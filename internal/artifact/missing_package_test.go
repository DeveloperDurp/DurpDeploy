package artifact

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchDistinguishesMissingPackage(t *testing.T) {
	for _, status := range []int{404, 401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
				}))
			t.Cleanup(upstream.Close)
			client := &Client{HTTP: upstream.Client(), TempDir: t.TempDir()}
			_, err := client.Fetch(t.Context(), Repository{
				URLTemplate: upstream.URL + "/{version}.zip", AuthType: "noauth",
			}, Pin{URL: upstream.URL + "/1.0.zip"})
			if !errors.Is(err, ErrFetch) ||
				errors.Is(err, ErrNotFound) != (status == 404) {
				t.Fatalf("status=%d error=%v", status, err)
			}
		})
	}
}

func TestFetchMissingPackageCleanupFailure(t *testing.T) {
	client := &Client{HTTP: &http.Client{Transport: failedCloseTransport{}}}
	_, err := client.Fetch(t.Context(), Repository{
		URLTemplate: "https://packages.example/{version}.zip", AuthType: "noauth",
	}, Pin{URL: "https://packages.example/1.0.zip"})
	if !errors.Is(err, ErrFetch) || !errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, ErrNotFound) {
		t.Fatalf("failed cleanup permitted a missing-package override: %v", err)
	}
}

type failedCloseTransport struct{}

func (failedCloseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound,
		Body: failedCloseBody{strings.NewReader("")}}, nil
}

type failedCloseBody struct{ io.Reader }

func (failedCloseBody) Close() error { return io.ErrUnexpectedEOF }
