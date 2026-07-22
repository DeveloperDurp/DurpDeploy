package notify

import (
	"context"
	"errors"
	"net/smtp"
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
	n := NewEmailNotifier(SMTPConfig{
		Host: "smtp.example.com",
		Port: "587",
		From: "durpdeploy@example.com",
	})

	var gotAddr, gotFrom string
	var gotTo []string
	n.sendMail = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		gotAddr, gotFrom, gotTo = addr, from, to
		return nil
	}

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
	if gotAddr != "smtp.example.com:587" {
		t.Fatalf("addr = %q", gotAddr)
	}
	if gotFrom != "durpdeploy@example.com" {
		t.Fatalf("from = %q", gotFrom)
	}
	if len(gotTo) != 2 || gotTo[0] != "a@example.com" ||
		gotTo[1] != "b@example.com" {
		t.Fatalf("to = %v", gotTo)
	}
}

func TestEmailNotifier_PropagatesSendError(t *testing.T) {
	n := NewEmailNotifier(SMTPConfig{Host: "smtp.example.com"})
	n.sendMail = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		return errors.New("connection refused")
	}

	_, err := n.Notify(context.Background(), events.Event{
		Message:      "x",
		NotifyEmails: []string{"a@example.com"},
	})
	if err == nil {
		t.Fatal("expected error to propagate from sendMail")
	}
}
