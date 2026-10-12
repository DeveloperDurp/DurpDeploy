//go:build e2e

package api_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/maintenance"
	"durpdeploy/internal/notify"
)

func TestStalledSMTPDeliveryAPIWebE2E(t *testing.T) {
	// Given: a configured SMTP peer accepts a connection without a greeting.
	f := newArtifactE2E(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	elapsed := make(chan time.Duration, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		listener.Close()
		start := time.Now()
		conn.SetDeadline(start.Add(15 * time.Second))
		io.Copy(io.Discard, conn)
		elapsed <- time.Since(start)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(f.h.repo)
	bus.Register(notify.NewEmailNotifier(notify.SMTPConfig{
		Host: host, Port: port, From: "fixture@example.com",
	}))
	bus.Register(artifactNotifier{f.done})
	f.h.runner.SetEventBus(bus)
	f.api(t, "PUT", f.base()+"/notifications", map[string]string{
		"notify_emails": "recipient@example.com",
	}, 200)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "check", "script_body": "true",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "smtp-timeout"}, 201), &release)
	// When: an API deployment publishes a notification to the stalled peer.
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{"release_id": release.ID,
			"environment_id": f.environment.ID}, 201), &deployment)
	select {
	case duration := <-elapsed:
		if duration > 12*time.Second {
			t.Fatalf("SMTP connection survived its deadline: %s", duration)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("SMTP delivery did not release its connection")
	}
	// Then: execution continues, and API/web history records a generic failure.
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	awaitNotificationHistory(t, f, "failed: delivery failed")
	if !strings.Contains(f.web(t, "GET", "/admin/notifications", nil, 200),
		"failed: delivery failed") {
		t.Fatal("web notification history omitted delivery failure")
	}
}

func TestBackupHealthRecoveryAPIWebE2E(t *testing.T) {
	// Given: an operator check fails once and then recovers.
	f := newArtifactE2E(t)
	marker := filepath.Join(t.TempDir(), "checked")
	quoted := "'" + strings.ReplaceAll(marker, "'", "'\"'\"'") + "'"
	t.Setenv("DURPDEPLOY_LITESTREAM_CHECK_COMMAND",
		fmt.Sprintf("test -e %s || { touch %s; exit 1; }", quoted, quoted))
	t.Setenv("DURPDEPLOY_LITESTREAM_CHECK_INTERVAL", "100ms")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// When: the real maintenance loop publishes failure and recovery events.
	if err := maintenance.StartLitestreamCheck(
		ctx,
		events.NewBus(f.h.repo),
	); err != nil {
		t.Fatal(err)
	}
	awaitNotificationHistory(t, f, "backup_healthy")
	cancel()
	// Then: both public surfaces expose the failure and recovery records.
	for _, body := range []string{
		string(f.api(t, "GET", "/api/v1/admin/notifications", nil, 200)),
		f.web(t, "GET", "/admin/notifications", nil, 200),
	} {
		if !strings.Contains(body, "backup_unhealthy") ||
			!strings.Contains(body, "backup_healthy") {
			t.Fatal("backup history omitted failure or recovery")
		}
	}
}

func awaitNotificationHistory(t *testing.T, f *artifactE2E, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body := f.api(t, "GET", "/api/v1/admin/notifications", nil, 200)
		if strings.Contains(string(body), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("notification history did not contain %q", marker)
}
