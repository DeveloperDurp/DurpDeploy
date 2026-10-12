package notify

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/events"
)

func TestEmailNotifier_VerifiedTLSHasNoWarning(t *testing.T) {
	tlsPeer := httptest.NewTLSServer(nil)
	defer tlsPeer.Close()
	if os.Getenv("DURPDEPLOY_SMTP_TLS_TEST") != "1" {
		// System roots are cached. Isolate the trusted fixture CA in a subprocess.
		roots := filepath.Join(t.TempDir(), "roots.pem")
		certificate := pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE", Bytes: tlsPeer.Certificate().Raw,
		})
		if err := os.WriteFile(roots, certificate, 0600); err != nil {
			t.Fatal(err)
		}
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary,
			"-test.run=^TestEmailNotifier_VerifiedTLSHasNoWarning$")
		cmd.Env = append(os.Environ(), "DURPDEPLOY_SMTP_TLS_TEST=1",
			"SSL_CERT_FILE="+roots, "SSL_CERT_DIR="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("trusted SMTP fixture: %v\n%s", err, output)
		}
		return
	}
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
		if err := conn.SetDeadline(
			time.Now().Add(3 * time.Second),
		); err != nil {
			done <- err
			return
		}
		done <- acceptVerifiedSMTP(conn, tlsPeer.TLS)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	notifier := NewEmailNotifier(SMTPConfig{
		Host: host, Port: port, From: "sender@example.test",
	})
	skipped, err := notifier.Notify(t.Context(), events.Event{
		NotifyEmails: []string{
			"recipient@example.test",
		},
		Message: "TLS fixture",
	})
	if err != nil || skipped {
		t.Fatalf("verified TLS delivery: skipped=%v err=%v", skipped, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func acceptVerifiedSMTP(conn net.Conn, config *tls.Config) error {
	proto := textproto.NewConn(conn)
	if err := proto.PrintfLine("220 fixture ready"); err != nil {
		return err
	}
	for _, exchange := range [][2]string{
		{"EHLO localhost", "250-fixture ready\r\n250 STARTTLS"},
		{"STARTTLS", "220 Start TLS"},
	} {
		if err := smtpExchange(proto, exchange[0], exchange[1]); err != nil {
			return err
		}
	}
	secure := tls.Server(conn, config)
	if err := secure.Handshake(); err != nil {
		return err
	}
	proto = textproto.NewConn(secure)
	for _, exchange := range [][2]string{
		{"EHLO localhost", "250 fixture ready"},
		{"MAIL FROM:<sender@example.test>", "250 OK"},
		{"RCPT TO:<recipient@example.test>", "250 OK"},
		{"DATA", "354 Send message"},
	} {
		if err := smtpExchange(proto, exchange[0], exchange[1]); err != nil {
			return err
		}
	}
	if _, err := io.Copy(io.Discard, proto.DotReader()); err != nil {
		return err
	}
	if err := proto.PrintfLine("250 Queued"); err != nil {
		return err
	}
	return smtpExchange(proto, "QUIT", "221 Bye")
}
