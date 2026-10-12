package notify

import (
	"context"
	"encoding/base64"
	"io"
	"net/textproto"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestEmailNotifier_SkipsWhenSMTPNotConfigured(t *testing.T) {
	n := NewEmailNotifier(SMTPConfig{})
	skipped, err := n.Notify(context.Background(), events.Event{
		Message:      "hi",
		NotifyEmails: []string{"a@example.com"},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !skipped {
		t.Fatal("expected skipped=true when SMTP host is not configured")
	}
}

func TestEmailNotifier_SkipsWhenNoRecipients(t *testing.T) {
	n := NewEmailNotifier(SMTPConfig{Host: "smtp.example.com"})
	skipped, err := n.Notify(context.Background(), events.Event{Message: "hi"})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !skipped {
		t.Fatal("expected skipped=true when there are no recipients")
	}
}

func TestEmailNotifier_SendsMailWithConfiguredRecipients(t *testing.T) {
	// Given: a real SMTP connection with two expected envelope recipients.
	message := make(chan string, 1)
	credentials := base64.StdEncoding.EncodeToString(
		[]byte("\x00fixture-user\x00fixture-pass"),
	)
	cfg := smtpTestServer(t, func(conn *textproto.Conn) error {
		if err := conn.PrintfLine("220 smtp.example.com"); err != nil {
			return err
		}
		for _, exchange := range [][2]string{
			{"EHLO localhost", "250-smtp.example.com\r\n250 AUTH PLAIN"},
			{"AUTH PLAIN " + credentials, "235 Authenticated"},
			{"MAIL FROM:<durpdeploy@example.com>", "250 OK"},
			{"RCPT TO:<a@example.com>", "250 OK"},
			{"RCPT TO:<b@example.com>", "250 OK"},
			{"DATA", "354 Send message"},
		} {
			if err := smtpExchange(conn, exchange[0], exchange[1]); err != nil {
				return err
			}
		}
		body, err := io.ReadAll(conn.DotReader())
		if err != nil {
			return err
		}
		message <- string(body)
		if err := conn.PrintfLine("250 Queued"); err != nil {
			return err
		}
		return smtpExchange(conn, "QUIT", "221 Bye")
	})
	cfg.User, cfg.Pass = "fixture-user", "fixture-pass"
	n := NewEmailNotifier(cfg)

	// When: the event is delivered.
	skipped, err := n.Notify(context.Background(), events.Event{
		Message:      "Deployment #1 succeeded",
		NotifyEmails: []string{"a@example.com", "b@example.com"},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if skipped {
		t.Fatal("expected skipped=false when SMTP and recipients are set")
	}
	// Then: the envelope and body reach the SMTP peer.
	if body := <-message; !strings.Contains(body, "Deployment #1 succeeded") {
		t.Fatalf("message = %q", body)
	}
}

func TestEmailNotifier_PropagatesSendError(t *testing.T) {
	// Given: the SMTP peer refuses delivery.
	cfg := smtpTestServer(t, func(conn *textproto.Conn) error {
		return conn.PrintfLine("554 Service unavailable")
	})
	n := NewEmailNotifier(cfg)

	// When: a notification is sent.
	_, err := n.Notify(context.Background(), events.Event{
		Message:      "x",
		NotifyEmails: []string{"a@example.com"},
	})
	// Then: the failure reaches the publisher.
	if err == nil {
		t.Fatal("expected error to propagate from sendMail")
	}
}
