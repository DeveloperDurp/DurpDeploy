package staging

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotWriteCancelsDuringContentCopy(t *testing.T) {
	const contentBytes = 128 << 10
	snapshot := snapshotFixture(t, map[string]string{
		"large": strings.Repeat("x", contentBytes),
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target := &cancelSnapshotWriter{cancel: cancel}
	err := snapshot.Write(ctx, target)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, target.written, contentBytes,
		"cancellation did not stop the content copy")
}

type cancelSnapshotWriter struct {
	cancel  context.CancelFunc
	written int
}

func (w *cancelSnapshotWriter) Write(data []byte) (int, error) {
	w.written += len(data)
	if w.written > 1024 {
		w.cancel()
	}
	return len(data), nil
}
