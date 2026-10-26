package artifact

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredDownloadLimit(t *testing.T) {
	data := zipFixture(t, "app.txt", 0600)
	for _, streaming := range []bool{false, true} {
		for _, delta := range []int64{-1, 0, 1} {
			t.Run(
				fmt.Sprintf("streaming=%t/delta=%d", streaming, delta),
				func(t *testing.T) {
					server, trusted := artifactTLSServer(
						t,
						http.HandlerFunc(
							func(w http.ResponseWriter, _ *http.Request) {
								if streaming {
									w.(http.Flusher).Flush()
								}
								_, _ = w.Write(data)
							},
						),
					)
					limits := config.Defaults().Limits.Artifact
					limits.DownloadBytes = int64(len(data)) + delta
					client := NewClientWithLimits(limits)
					limits.DownloadBytes = 1 // The client must retain its snapshot.
					transport := client.HTTP.Transport.(*http.Transport)
					transport.TLSClientConfig = trusted.HTTP.Transport.(*http.Transport).TLSClientConfig.Clone()
					t.Cleanup(transport.CloseIdleConnections)
					client.TempDir = t.TempDir()
					download, err := client.Fetch(t.Context(), Repository{
						URLTemplate: server.URL + "/{version}.zip", AuthType: "noauth",
					}, Pin{URL: server.URL + "/1.zip"})
					if delta < 0 {
						require.ErrorIs(t, err, ErrFetch)
						require.Empty(t, download.Path)
					} else {
						require.NoError(t, err)
						require.Equal(t, int64(len(data)), download.Size)
						require.NoError(t, os.Remove(download.Path))
					}
					entries, err := os.ReadDir(client.TempDir)
					require.NoError(t, err)
					require.Empty(t, entries)
				},
			)
		}
	}
}

func TestRaisedDownloadZIPExtraction(t *testing.T) {
	data := zipFixture(t, "app.txt", 0600)
	filename := filepath.Join(t.TempDir(), "large.zip")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.Seek(MaxDownload+1, io.SeekStart)
	require.NoError(t, err)
	_, err = file.Write(data)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.Error(t, ValidateZIP(t.Context(), filename))
	limit := MaxDownload + 1 + int64(len(data))
	limits := config.Defaults().Limits.Artifact
	limits.DownloadBytes = limit
	require.NoError(
		t,
		ValidateZIPWithLimits(t.Context(), filename, limits),
	)
	directory, err := ExtractZIPWithLimits(t.Context(), filename, limits)
	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(directory, "app.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, contents)
	require.NoError(t, os.RemoveAll(directory))
	limits.DownloadBytes--
	_, err = ExtractZIPWithLimits(t.Context(), filename, limits)
	require.Error(t, err)
	entries, err := os.ReadDir(filepath.Dir(filename))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}
