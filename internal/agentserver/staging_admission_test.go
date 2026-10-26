package agentserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func startedStagingAdmission(
	t *testing.T,
	settings ...config.Config,
) (agentFixture, int64, string) {
	t.Helper()
	f := newAgentFixtureWithDSN(
		t,
		":memory:?_pragma=foreign_keys(1)",
		nil,
		settings...)
	f.repo.DB.SetMaxOpenConns(1)
	f.client.Timeout = 10 * time.Second
	id, claim := claimedRemoteStep(t, f)
	seedStageInput(t, f, id)
	response := postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/start", id), claim)
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	var identity struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &identity); err != nil {
		t.Fatal(err)
	}
	return f, id, identity.Token
}

// Add a real pinned mTLS peer with a started fan-out run for the same input.
func stagingAdmissionPeer(
	t *testing.T,
	f agentFixture,
	id int64,
	name string,
) agentFixture {
	t.Helper()
	peer, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent")
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: peer.Certificate.Certificate[0],
	}))
	if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO agents
(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity)
VALUES (?,?,'https://agent','active',?,?,'fixture')`,
		name, name, cert, peer.Fingerprint.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO agent_pairings
(agent_id,pairing_code_hash,agent_public_identity,agent_pin,state,expires_at,
paired_at,server_public_identity,server_pin,encrypted_identity)
SELECT ?,randomblob(32),?,?,'paired',1,1,'fixture',server_pin,'fixture'
FROM agent_pairings WHERE agent_id='test-agent'`,
		name, cert, peer.Fingerprint.String()); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("admission-" + name))
	if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO remote_step_runs
(deployment_id,step_index,agent_id,state,claim_token_hash,claim_expires_at,last_heartbeat_at)
SELECT deployment_id,step_index,?,'started',?,claim_expires_at,last_heartbeat_at
FROM remote_step_runs WHERE deployment_id=? AND agent_id='test-agent'`,
		name, hash[:], id); err != nil {
		t.Fatal(err)
	}
	transport := f.client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{peer.Certificate}
	t.Cleanup(transport.CloseIdleConnections)
	client := *f.client
	client.Transport = transport
	f.client = &client
	return f
}

type stagingAdmissionBody struct {
	io.ReadCloser
	started chan<- string
	name    string
	once    sync.Once
}

func (b *stagingAdmissionBody) Read(data []byte) (int, error) {
	b.once.Do(func() { b.started <- b.name })
	return b.ReadCloser.Read(data)
}

type stagingAdmissionTrap struct {
	io.ReadCloser
	reads *atomic.Int64
}

type stagingAdmissionZeros struct{}

func (stagingAdmissionZeros) Read(data []byte) (int, error) {
	clear(data)
	return len(data), nil
}

type stagingAdmissionQueries struct {
	*sql.DB
	chunks atomic.Int64
	fail   atomic.Bool
}

func (p *stagingAdmissionQueries) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if strings.Contains(query, "-- name: GetStagingChunk") {
		p.chunks.Add(1)
		if p.fail.Load() {
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return p.DB.QueryRowContext(ctx, query, args...)
		}
	}
	return p.DB.QueryRowContext(ctx, query, args...)
}

func (b *stagingAdmissionTrap) Read(data []byte) (int, error) {
	b.reads.Add(1)
	return b.ReadCloser.Read(data)
}

func (f agentFixture) stagingAdmissionRequest(
	t *testing.T,
	ctx context.Context,
	id int64,
	token, action, label string,
	data []byte,
) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf(
			"%s/agent/v1/deployments/%d/staging/%s",
			f.server.URL,
			id,
			action,
		),
		bytes.NewReader(data),
	)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-DurpDeploy-Claim", token)
	r.Header.Set("X-Test-Transfer", label)
	return r
}

func TestAgentStagingAdmissionGlobalAndPerAgent(t *testing.T) {
	f, id, token := startedStagingAdmission(t)
	queries := &stagingAdmissionQueries{DB: f.repo.DB}
	f.repo.Queries = db.New(queries)
	peers := []agentFixture{f}
	for i := range 4 {
		peers = append(
			peers,
			stagingAdmissionPeer(t, f, id, fmt.Sprintf("peer-%d", i)),
		)
	}
	started, finished := make(chan string, 4), make(chan string, 4)
	var rejectedReads atomic.Int64
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			label := r.Header.Get("X-Test-Transfer")
			if strings.HasPrefix(label, "hold-") {
				r.Body = &stagingAdmissionBody{
					ReadCloser: r.Body,
					started:    started,
					name:       label,
				}
				defer func() { finished <- label }()
			} else if label == "probe" {
				r.Body = &stagingAdmissionTrap{ReadCloser: r.Body, reads: &rejectedReads}
			}
			router.ServeHTTP(w, r)
		}),
	)
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	target := f
	target.server = listener
	t.Cleanup(listener.Close)
	data := stageArchive(t, map[string]string{"result": "valid"})
	cancels := target.holdStagingAdmissionUploads(t, peers, id, token, data)
	for range 4 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("four distinct agents were not admitted")
		}
	}
	target.checkStagingAdmissionProbes(t, peers, id, token, data)
	if rejectedReads.Load() != 0 {
		t.Fatalf("rejected requests read body %d times", rejectedReads.Load())
	}
	if queries.chunks.Load() != 0 {
		t.Fatal("rejected download loaded encrypted snapshot chunks")
	}
	// Known oversized uploads retain 413, even while admission is full.
	transport := f.client.Transport.(*http.Transport).Clone()
	transport.ExpectContinueTimeout = time.Second
	defer transport.CloseIdleConnections()
	client := *f.client
	client.Transport = transport
	r := target.stagingAdmissionRequest(
		t,
		t.Context(),
		id,
		token,
		"upload",
		"probe",
		nil,
	)
	r.Body = io.NopCloser(
		io.LimitReader(stagingAdmissionZeros{}, staging.MaxArchive+1),
	)
	r.GetBody = nil
	r.ContentLength = staging.MaxArchive + 1
	r.Header.Set("Expect", "100-continue")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 413 {
		t.Fatalf("oversized status=%d", response.StatusCode)
	}
	// A real disconnect releases exactly one permit for an independent peer.
	cancels[0]()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnected upload retained permit")
	}
	peer := peers[4]
	peer.server = listener
	stageRequest(t, peer, id, "admission-peer-3", "download", nil, 200)
	stageRequest(t, peer, id, "admission-peer-3", "upload", data, 204)
	stageRequest(t, peer, id, "admission-peer-3", "upload", data, 204)
	for _, cancel := range cancels[1:] {
		cancel()
	}
	for range 3 {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("upload cleanup did not finish")
		}
	}
	for i := range 4 {
		done, ok := f.repo.TryBeginStagingTransfer(
			db.GetStagingSnapshotParams{
				Producer: fmt.Sprintf("recovery-%d", i),
			},
		)
		if !ok {
			t.Fatal("completed requests retained global admission")
		}
		defer done()
	}
}

func (target agentFixture) holdStagingAdmissionUploads(
	t *testing.T,
	peers []agentFixture,
	id int64,
	token string,
	data []byte,
) []context.CancelFunc {
	t.Helper()
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	for i, peer := range peers[:4] {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		peerToken := token
		if i > 0 {
			peerToken = fmt.Sprintf("admission-peer-%d", i-1)
		}
		r := target.stagingAdmissionRequest(
			t,
			ctx,
			id,
			peerToken,
			"upload",
			fmt.Sprintf("hold-%d", i),
			data,
		)
		input, output := io.Pipe()
		r.Body, r.GetBody = input, nil
		r.ContentLength = int64(len(data))
		go func() {
			defer func() { _ = output.Close() }()
			_, _ = output.Write(data[:512])
			<-ctx.Done()
		}()
		go func() {
			response, err := peer.client.Do(r)
			if err == nil {
				response.Body.Close()
			}
		}()
	}
	return cancels
}

func (target agentFixture) checkStagingAdmissionProbes(
	t *testing.T,
	peers []agentFixture,
	id int64,
	token string,
	data []byte,
) {
	t.Helper()
	for _, probe := range []struct {
		peer          int
		action, token string
		want          int
	}{
		{0, "upload", token, 429}, {0, "download", token, 429},
		{4, "upload", "admission-peer-3", 429}, {4, "download", "admission-peer-3", 429},
		{0, "upload", "wrong", 409},
	} {
		label := "probe"
		if probe.want != 429 {
			label = "auth"
		}
		r := target.stagingAdmissionRequest(
			t,
			t.Context(),
			id,
			probe.token,
			probe.action,
			label,
			data,
		)
		response, err := peers[probe.peer].client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != probe.want ||
			(probe.want == 429 && response.Header.Get("Retry-After") != "1") {
			t.Fatalf(
				"%s: status=%d retry=%q",
				probe.action,
				response.StatusCode,
				response.Header.Get("Retry-After"),
			)
		}
	}
}

func TestAgentStagingAdmissionReleasesAfterDownloadFailure(t *testing.T) {
	f, id, token := startedStagingAdmission(t)
	queries := &stagingAdmissionQueries{DB: f.repo.DB}
	queries.fail.Store(true)
	f.repo.Queries = db.New(queries)
	stageRequest(t, f, id, token, "download", nil, 422)
	queries.fail.Store(false)
	stageRequest(t, f, id, token, "download", nil, 200)
}

func TestAgentStagingAdmissionReleasesAfterPublicationFailure(t *testing.T) {
	f, id, token := startedStagingAdmission(t)
	stageRequest(t, f, id, token, "upload", []byte("invalid"), 422)
	if _, err := f.repo.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_staging_publication
BEFORE INSERT ON deployment_staging_chunks BEGIN SELECT RAISE(ABORT,'injected publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	data := stageArchive(t, map[string]string{"result": "valid"})
	stageRequest(t, f, id, token, "upload", data, 500)
	var count int
	if err := f.repo.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deployment_staging_snapshots WHERE deployment_id=? AND producer='test-agent'`, id).Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("failed publication retained metadata=%d error=%v", count, err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(), "DROP TRIGGER fail_staging_publication"); err != nil {
		t.Fatal(err)
	}
	stageRequest(t, f, id, token, "upload", data, 204)
	stageRequest(t, f, id, token, "upload", data, 204)
	done, ok := f.repo.TryBeginStagingTransfer(
		db.GetStagingSnapshotParams{Producer: "test-agent"},
	)
	if !ok {
		t.Fatal("publication retained permit")
	}
	done()
}

type stagingAdmissionWriter struct {
	http.ResponseWriter
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (w *stagingAdmissionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *stagingAdmissionWriter) Write(_ []byte) (int, error) {
	w.once.Do(func() {
		w.WriteHeader(200)
		_ = http.NewResponseController(w.ResponseWriter).Flush()
		close(w.started)
	})
	<-w.ctx.Done()
	return 0, w.ctx.Err()
}

func TestAgentStagingAdmissionReleasesAfterDownloadDisconnect(t *testing.T) {
	f, id, token := startedStagingAdmission(t)
	started, finished := make(chan struct{}), make(chan struct{})
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(finished)
			router.ServeHTTP(
				&stagingAdmissionWriter{
					ResponseWriter: w,
					ctx:            r.Context(),
					started:        started,
				},
				r,
			)
		}),
	)
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	target := f
	target.server = listener
	defer listener.Close()
	r := target.stagingAdmissionRequest(
		t,
		t.Context(),
		id,
		token,
		"download",
		"",
		nil,
	)
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download not admitted")
	}
	if done, ok := f.repo.TryBeginStagingTransfer(db.GetStagingSnapshotParams{Producer: "test-agent"}); ok {
		done()
		t.Fatal("download did not hold agent permit")
	}
	response.Body.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("download disconnect retained permit")
	}
	stageRequest(t, f, id, token, "download", nil, 200)
}
