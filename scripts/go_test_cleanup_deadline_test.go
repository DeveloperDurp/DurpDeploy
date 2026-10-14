//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestCleanupRecoveryResourcesBeforeStalledTestcontainer(t *testing.T) {
	session := "durpdeploy-go-test.fixture"
	namespace := "docker:" + strings.ReplaceAll(session, ".", "-") + "-restore"
	var inspections, deletions, recoveryRemoved atomic.Int32
	blocked := removalHandler(t, http.StatusConflict, false,
		&inspections, &deletions)
	server := httptest.NewServer(recoveryBeforeConflictHandler(t,
		namespace, blocked, &recoveryRemoved))
	defer server.Close()
	api, err := client.New(client.WithHost(server.URL),
		client.WithAPIVersion("1.44"))
	require.NoError(t, err)
	defer func() { require.NoError(t, api.Close()) }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	err = cleanup(ctx, &testcontainers.DockerClient{Client: api},
		[]string{session})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(
		t,
		int32(2),
		recoveryRemoved.Load(),
		"remove application resources before the Testcontainers timeout",
	)
}

func recoveryBeforeConflictHandler(t *testing.T, namespace string,
	blocked http.HandlerFunc, removed *atomic.Int32,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete &&
			(strings.HasSuffix(r.URL.Path, "/containers/recovery") ||
				strings.HasSuffix(r.URL.Path, "/volumes/recovery")) {
			removed.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(r.URL.Query().Get("filters"), namespace) {
			labels := map[string]string{"io.durpdeploy.namespace": namespace}
			var response any
			if strings.HasSuffix(r.URL.Path, "/containers/json") {
				response = []map[string]any{
					{"Id": "recovery", "Labels": labels},
				}
			} else {
				response = map[string]any{"Volumes": []map[string]any{
					{"Name": "recovery", "Labels": labels},
				}}
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
			return
		}
		blocked(w, r)
	}
}

func TestCleanupVolumeAfterStalledRecoveryContainer(t *testing.T) {
	session := "durpdeploy-go-test.fixture"
	namespace := "docker:" + strings.ReplaceAll(session, ".", "-") + "-restore"
	var inspections, deletions, recoveryRemoved atomic.Int32
	blocked := removalHandler(t, http.StatusConflict, false,
		&inspections, &deletions)
	base := recoveryBeforeConflictHandler(
		t,
		namespace,
		blocked,
		&recoveryRemoved,
	)
	server := httptest.NewServer(stalledRecoveryHandler(t, base))
	defer server.Close()
	api, err := client.New(client.WithHost(server.URL),
		client.WithAPIVersion("1.44"))
	require.NoError(t, err)
	defer func() { require.NoError(t, api.Close()) }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	err = cleanup(ctx, &testcontainers.DockerClient{Client: api},
		[]string{session})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int32(1), recoveryRemoved.Load(),
		"remove the staging volume after the application container stalls")
}

func stalledRecoveryHandler(
	t *testing.T,
	base http.HandlerFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/recovery") ||
			strings.HasSuffix(r.URL.Path, "/containers/recovery/json") {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusConflict)
			}
			if err := json.NewEncoder(w).Encode(map[string]string{
				"message": "application removal is already in progress",
			}); err != nil {
				t.Error(err)
			}
			return
		}
		base(w, r)
	}
}
