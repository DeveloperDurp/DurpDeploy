package artifact

import (
	"errors"
	"net/http"
	"net/http/httptest"
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
