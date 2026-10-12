package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"durpdeploy/internal/events"
)

func smtpTestServer(
	t *testing.T,
	serve func(*textproto.Conn) error,
) SMTPConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(
			time.Now().Add(3 * time.Second),
		); err != nil {
			done <- err
			return
		}
		done <- serve(textproto.NewConn(conn))
	}()
	t.Cleanup(func() {
		listener.Close()
		if err := <-done; err != nil {
			t.Errorf("SMTP peer: %v", err)
		}
	})
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return SMTPConfig{Host: host, Port: port, From: "durpdeploy@example.com"}
}

func smtpExchange(conn *textproto.Conn, want, response string) error {
	line, err := conn.ReadLine()
	if err != nil {
		return err
	}
	if line != want {
		return fmt.Errorf("SMTP command = %q, want %q", line, want)
	}
	return conn.PrintfLine("%s", response)
}

func TestEmailNotifier_RejectsUntrustedTLS(t *testing.T) {
	// Given: a peer offers STARTTLS with an untrusted certificate.
	tlsPeer := httptest.NewTLSServer(nil)
	defer tlsPeer.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		proto := textproto.NewConn(conn)
		if err := proto.PrintfLine("220 smtp.example.com"); err != nil {
			done <- err
			return
		}
		for _, exchange := range [][2]string{
			{"EHLO localhost", "250-smtp.example.com\r\n250 STARTTLS"},
			{"STARTTLS", "220 Start TLS"},
		} {
			if err := smtpExchange(
				proto,
				exchange[0],
				exchange[1],
			); err != nil {
				done <- err
				return
			}
		}
		secure := tls.Server(conn, tlsPeer.TLS)
		done <- secure.HandshakeContext(t.Context())
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	notifier := NewEmailNotifier(SMTPConfig{Host: host, Port: port})
	// When: the client upgrades the SMTP connection.
	_, err = notifier.Notify(context.Background(), events.Event{
		NotifyEmails: []string{"recipient@example.com"},
	})
	// Then: certificate verification fails before any message is sent.
	var untrusted x509.UnknownAuthorityError
	if !errors.As(err, &untrusted) {
		t.Fatalf("TLS error = %v, want unknown certificate authority", err)
	}
	if err := <-done; err == nil {
		t.Fatal("SMTP peer completed an untrusted TLS handshake")
	}
}
