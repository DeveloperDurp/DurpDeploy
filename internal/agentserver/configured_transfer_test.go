package agentserver_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredStagingAdmissionAPIE2E(t *testing.T) {
	for _, limits := range []config.TransferLimits{
		{Global: 2, PerAgent: 1, IdleTimeoutSeconds: 300},
		{Global: 6, PerAgent: 3, IdleTimeoutSeconds: 300},
	} {
		t.Run(
			fmt.Sprintf("%d/%d", limits.Global, limits.PerAgent),
			func(t *testing.T) {
				checkConfiguredStagingAdmission(t, limits)
			},
		)
	}
}

func checkConfiguredStagingAdmission(
	t *testing.T,
	limits config.TransferLimits,
) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Transfer = limits
	require.NoError(t, settings.Limits.Validate())
	f, id, token := startedStagingAdmission(t, settings)
	peers := []agentFixture{f}
	for i := range 2 {
		peers = append(peers, stagingAdmissionPeer(t, f, id,
			fmt.Sprintf("configured-%d", i)))
	}
	started := make(chan string, limits.Global)
	finished := make(chan struct{}, limits.Global)
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Transfer") == "hold" {
				r.Body = &stagingAdmissionBody{
					ReadCloser: r.Body, started: started, name: "hold",
				}
				defer func() { finished <- struct{}{} }()
			}
			router.ServeHTTP(w, r)
		}))
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	t.Cleanup(listener.Close)
	f.server = listener
	data := stageArchive(t, map[string]string{"result": "configured"})
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	for i := int64(0); i < limits.Global; i++ {
		peerIndex := int(i / limits.PerAgent)
		claim := token
		if peerIndex > 0 {
			claim = fmt.Sprintf("admission-configured-%d", peerIndex-1)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		request := f.stagingAdmissionRequest(
			t,
			ctx,
			id,
			claim,
			"upload",
			"hold",
			nil,
		)
		input, output := io.Pipe()
		request.Body, request.GetBody = input, nil
		request.ContentLength = int64(len(data))
		go func() {
			defer func() { _ = output.Close() }()
			_, _ = output.Write(data[:512])
			<-ctx.Done()
		}()
		go func() {
			response, err := peers[peerIndex].client.Do(request)
			if err == nil {
				response.Body.Close()
			}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("configured transfer was not admitted")
		}
		if i+1 == limits.PerAgent {
			checkConfiguredTransferRejected(t, f, f, id, token, data)
		}
	}
	checkConfiguredTransferRejected(
		t,
		f,
		peers[2],
		id,
		"admission-configured-1",
		data,
	)
	for _, cancel := range cancels {
		cancel()
	}
	for range limits.Global {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("disconnect retained a staging transfer")
		}
	}
	stageRequest(t, f, id, token, "upload", data, 204)
	stageRequest(t, f, id, token, "download", nil, 200)
}

func checkConfiguredTransferRejected(
	t *testing.T,
	target, peer agentFixture,
	id int64,
	token string,
	data []byte,
) {
	t.Helper()
	for _, action := range []string{"upload", "download"} {
		request := target.stagingAdmissionRequest(t, t.Context(), id, token,
			action, "probe", data)
		response, err := peer.client.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
		require.Equal(t, "1", response.Header.Get("Retry-After"))
	}
}

func TestConfiguredStagingIdleAPIE2E(t *testing.T) {
	settings := config.Defaults()
	settings.Limits.Transfer.IdleTimeoutSeconds = 1
	f, id, token := startedStagingAdmission(t, settings)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input, output := io.Pipe()
	defer func() { _ = output.Close() }()
	request := f.stagingAdmissionRequest(
		t,
		ctx,
		id,
		token,
		"upload",
		"idle",
		nil,
	)
	request.Body, request.GetBody, request.ContentLength = input, nil, 1024
	go func() { _, _ = io.WriteString(output, strings.Repeat("x", 512)) }()
	started := time.Now()
	response, err := f.client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.GreaterOrEqual(t, time.Since(started), time.Second)
	require.Less(t, time.Since(started), 5*time.Second)
	data := stageArchive(t, map[string]string{"result": "after-idle"})
	stageRequest(t, f, id, token, "upload", data, 204)
}
