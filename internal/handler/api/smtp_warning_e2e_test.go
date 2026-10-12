//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/notify"
)

func TestSMTPPlaintextWarningAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	publishPlaintextNotification(t, f)
	var rows []db.ListNotificationEventsRow
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/admin/notifications",
		nil, 200), &rows)
	if len(rows) != 1 {
		t.Fatalf("history rows = %d, want 1", len(rows))
	}
	var results map[string]string
	if err := json.Unmarshal([]byte(rows[0].Results), &results); err != nil {
		t.Fatal(err)
	}
	if results["email"] != events.SMTPPlaintextStatus {
		t.Fatalf("delivery status = %q", results["email"])
	}
	body := f.web(t, "GET", "/admin/notifications", nil, 200)
	if !strings.Contains(body, events.SMTPPlaintextStatus) ||
		!strings.Contains(body, "badge-warning") {
		t.Fatal("web history omitted the plaintext delivery warning")
	}
}

func publishPlaintextNotification(t *testing.T, f *artifactE2E) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(
			time.Now().Add(5 * time.Second),
		); err != nil {
			done <- err
			return
		}
		done <- acceptPlaintextNotification(textproto.NewConn(conn))
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.api(t, "PUT", f.base()+"/notifications", map[string]string{
		"notify_emails": "recipient@example.test",
	}, 200)
	bus := events.NewBus(f.h.repo)
	bus.Register(notify.NewEmailNotifier(notify.SMTPConfig{
		Host: host, Port: port, From: "sender@example.test",
	}))
	bus.Publish(t.Context(), events.Event{
		Type: events.DeploymentSucceeded, ProjectID: f.project.ID,
		Message: "SMTP warning fixture",
	})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func acceptPlaintextNotification(conn *textproto.Conn) error {
	if err := conn.PrintfLine("220 fixture ready"); err != nil {
		return err
	}
	for _, step := range [][2]string{
		{"EHLO localhost", "250 fixture ready"},
		{"MAIL FROM:<sender@example.test>", "250 OK"},
		{"RCPT TO:<recipient@example.test>", "250 OK"},
		{"DATA", "354 Send message"},
	} {
		line, err := conn.ReadLine()
		if err != nil {
			return err
		}
		if line != step[0] {
			return fmt.Errorf("SMTP command = %q, want %q", line, step[0])
		}
		if err := conn.PrintfLine("%s", step[1]); err != nil {
			return err
		}
	}
	body, err := io.ReadAll(conn.DotReader())
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), "SMTP warning fixture") {
		return fmt.Errorf("SMTP peer did not receive the message")
	}
	if err := conn.PrintfLine("250 Queued"); err != nil {
		return err
	}
	line, err := conn.ReadLine()
	if err != nil {
		return err
	}
	if line != "QUIT" {
		return fmt.Errorf("SMTP command = %q, want QUIT", line)
	}
	return conn.PrintfLine("221 Bye")
}
