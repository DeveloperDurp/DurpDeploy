package agentserver_test

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
	"durpdeploy/internal/testutil"
)

type stagingEncryptionStats struct {
	Spools   int
	Smallest int64
}

func stagingEncryptionSpools() map[string]int64 {
	files := make(map[string]int64)
	entries, _ := os.ReadDir("/proc/self/fd")
	for _, entry := range entries {
		path := filepath.Join("/proc/self/fd", entry.Name())
		name, err := os.Readlink(path)
		if err != nil || !strings.Contains(name, "durpdeploy-gate-") {
			continue
		}
		if info, err := os.Stat(path); err == nil {
			files[name] = info.Size()
		}
	}
	return files
}

type stagingEncryptionPause struct {
	armed   atomic.Bool
	ready   chan stagingEncryptionStats
	done    chan struct{}
	abandon chan struct{}
	stop    sync.Once
}

func newStagingEncryptionPause(t *testing.T) *stagingEncryptionPause {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("encryption spool observation requires Linux procfs")
	}
	if _, err := os.ReadDir("/proc/self/fd"); err != nil {
		t.Fatal(err)
	}
	return &stagingEncryptionPause{
		ready:   make(chan stagingEncryptionStats, 1),
		done:    make(chan struct{}),
		abandon: make(chan struct{}),
	}
}

func (p *stagingEncryptionPause) release() { p.stop.Do(func() { close(p.abandon) }) }

func (p *stagingEncryptionPause) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/staging/upload") ||
			!p.armed.CompareAndSwap(false, true) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer close(p.done)
		probe := &stagingEncryptionContext{
			Context:  ctx,
			pause:    p,
			cancel:   cancel,
			baseline: stagingEncryptionSpools(),
		}
		next.ServeHTTP(w, r.WithContext(probe))
	})
}

// The test pauses at an observed context check after encrypted bytes exist.
// This avoids racing a timer against encryption or adding a production hook.
type stagingEncryptionContext struct {
	context.Context
	pause    *stagingEncryptionPause
	cancel   context.CancelFunc
	baseline map[string]int64
	once     sync.Once
}

func (ctx *stagingEncryptionContext) Err() error {
	if err := ctx.Context.Err(); err != nil {
		return err
	}
	stats := stagingEncryptionStats{Smallest: int64(^uint64(0) >> 1)}
	for name, size := range stagingEncryptionSpools() {
		if _, exists := ctx.baseline[name]; !exists {
			stats.Spools++
			stats.Smallest = min(stats.Smallest, size)
		}
	}
	// A fully read input and canonical TAR precede the encrypted spool.
	if stats.Spools == 3 && stats.Smallest > 0 {
		ctx.once.Do(func() {
			ctx.pause.ready <- stats
			select {
			case <-ctx.Done():
			case <-ctx.pause.abandon:
				ctx.cancel()
			}
		})
	}
	return ctx.Context.Err()
}

func stagingEncryptionArchive(t *testing.T) (*os.File, string) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "encryption-input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, file) })
	w := tar.NewWriter(file)
	if err := w.WriteHeader(&tar.Header{Name: "payload", Mode: 0644, Size: 32 << 20}); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(w, hash), stagingAdmissionZeros{}, 32<<20); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return file, hex.EncodeToString(hash.Sum(nil))
}

func TestAgentStagingEncryptionCancellation(t *testing.T) {
	f, id, token := startedStagingAdmission(t)
	f.client.Timeout = 30 * time.Second
	pause := newStagingEncryptionPause(t)
	listener := httptest.NewUnstartedServer(pause.wrap(f.server.Config.Handler))
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	t.Cleanup(listener.Close)
	defer pause.release()
	file, checksum := stagingEncryptionArchive(t)
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	upload := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(
			ctx,
			"POST",
			fmt.Sprintf(
				"%s/agent/v1/deployments/%d/staging/upload",
				listener.URL,
				id,
			),
			io.NewSectionReader(file, 0, info.Size()),
		)
		if err != nil {
			return err
		}
		req.ContentLength = info.Size()
		req.Header.Set("X-DurpDeploy-Claim", token)
		resp, err := f.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			return fmt.Errorf("upload status=%d", resp.StatusCode)
		}
		return nil
	}
	before := stagingEncryptionSpools()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- upload(ctx) }()
	var stats stagingEncryptionStats
	select {
	case stats = <-pause.ready:
	case <-time.After(30 * time.Second):
		t.Fatal("upload did not reach encryption")
	}
	started := time.Now()
	cancel()
	select {
	case <-pause.done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled encryption retained handler/spools")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("client cancellation: %v", err)
	}
	t.Logf(
		"canceled with %d spools, smallest=%d bytes; cleanup=%s",
		stats.Spools,
		stats.Smallest,
		time.Since(started),
	)
	if stats.Smallest < 1<<20 || stats.Smallest > 2<<20 {
		t.Fatalf("encryption continued beyond its first chunk: %+v", stats)
	}
	for name := range stagingEncryptionSpools() {
		if _, exists := before[name]; !exists {
			t.Fatal("canceled upload retained temporary spool")
		}
	}
	key := db.GetStagingSnapshotParams{DeploymentID: id, Producer: "test-agent"}
	if _, err := f.repo.Queries.GetStagingSnapshot(t.Context(), key); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("canceled snapshot published: %v", err)
	}
	var chunks int
	if err := f.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployment_staging_chunks WHERE deployment_id=? AND producer='test-agent'", id).Scan(&chunks); err != nil ||
		chunks != 0 {
		t.Fatalf("canceled chunks=%d error=%v", chunks, err)
	}
	done, ok := f.repo.TryBeginStagingTransfer(key)
	if !ok {
		t.Fatal("cancellation retained admission")
	}
	done()
	if err := upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.repo.LoadStaging(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, loaded)
	snapshot, err := staging.Read(t.Context(), loaded)
	if err != nil || snapshot.Entries["payload"].Size != 32<<20 ||
		snapshot.Entries["payload"].SHA256 != checksum {
		t.Fatalf("complete retry: %v", err)
	}
	if err := upload(t.Context()); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
}
