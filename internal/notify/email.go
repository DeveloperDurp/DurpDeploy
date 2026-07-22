package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"time"

	"durpdeploy/internal/events"
)

// SMTPConfig holds the server-wide SMTP settings used to send email
// notifications. An empty Host disables email delivery entirely (every
// EmailNotifier.Notify call skips).
type SMTPConfig struct {
	Host string
	Port string
	From string
	User string
	Pass string
}

// LoadSMTPConfigFromEnv reads the server-wide SMTP settings from the
// environment. There is no per-project SMTP server — only the recipient
// list (project.notify_emails) varies per project.
func LoadSMTPConfigFromEnv() SMTPConfig {
	return SMTPConfig{
		Host: os.Getenv("DURPDEPLOY_SMTP_HOST"),
		Port: os.Getenv("DURPDEPLOY_SMTP_PORT"),
		From: os.Getenv("DURPDEPLOY_SMTP_FROM"),
		User: os.Getenv("DURPDEPLOY_SMTP_USER"),
		Pass: os.Getenv("DURPDEPLOY_SMTP_PASS"),
	}
}

// EmailNotifier sends deployment lifecycle events to a project's configured
// recipient list over SMTP. It skips (rather than fails) when SMTP isn't
// configured server-wide or the project has no recipients.
type EmailNotifier struct {
	cfg SMTPConfig
	// sendMail is swapped out in tests; defaults to smtp.SendMail.
	sendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

func NewEmailNotifier(cfg SMTPConfig) *EmailNotifier {
	return &EmailNotifier{cfg: cfg, sendMail: smtp.SendMail}
}

func (e *EmailNotifier) Name() string { return "email" }

func (e *EmailNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if e.cfg.Host == "" || len(event.NotifyEmails) == 0 {
		return true, nil
	}

	addr := e.cfg.Host
	if e.cfg.Port != "" {
		addr = e.cfg.Host + ":" + e.cfg.Port
	}
	var auth smtp.Auth
	if e.cfg.User != "" {
		auth = smtp.PlainAuth("", e.cfg.User, e.cfg.Pass, e.cfg.Host)
	}

	msg := fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: durpdeploy notification\r\n"+
			"Date: %s\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: 8bit\r\n"+
			"\r\n"+
			"%s\r\n",
		e.cfg.From,
		strings.Join(event.NotifyEmails, ", "),
		time.Now().Format(time.RFC1123Z),
		event.Message,
	)

	if err := e.sendMail(
		addr,
		auth,
		e.cfg.From,
		event.NotifyEmails,
		[]byte(msg),
	); err != nil {
		return false, err
	}
	return false, nil
}
