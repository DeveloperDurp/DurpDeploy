package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"

	"durpdeploy/internal/events"
)

func (e *EmailNotifier) sendMail(
	ctx context.Context,
	recipients []string,
	message []byte,
) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addr := e.cfg.Host
	if e.cfg.Port != "" {
		addr = net.JoinHostPort(e.cfg.Host, e.cfg.Port)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	//nolint:errcheck // SMTP operations and Quit report errors before cleanup.
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP deadline: %w", err)
	}
	stop := context.AfterFunc(ctx, func() {
		// Cancellation closes the transport; the SMTP operation reports failure.
		_ = conn.Close()
	})
	defer stop()
	client, err := smtp.NewClient(conn, e.cfg.Host)
	if err != nil {
		return fmt.Errorf("read SMTP greeting: %w", err)
	}
	//nolint:errcheck // A successful Quit has already closed the SMTP connection.
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("send SMTP greeting: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{
			ServerName: e.cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if e.cfg.User != "" {
		auth := smtp.PlainAuth("", e.cfg.User, e.cfg.Pass, e.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err := client.Mail(e.cfg.From); err != nil {
		return fmt.Errorf("send SMTP sender: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("send SMTP recipient: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP session: %w", err)
	}
	if _, secure := client.TLSConnectionState(); !secure {
		return events.SMTPPlaintextWarning{}
	}
	return nil
}
