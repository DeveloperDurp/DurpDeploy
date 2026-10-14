//go:build e2e && backuprestore && linux

package api_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

type restoreMail struct {
	body string
	err  error
}

type restoreSMTP struct {
	address  string
	messages chan restoreMail
}

func restoreSMTPFixture(t *testing.T) *restoreSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &restoreSMTP{listener.Addr().String(), make(chan restoreMail, 16)}
	done := make(chan struct{})
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("SMTP fixture did not stop")
		}
	})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				f.messages <- restoreMail{err: err}
				return
			}
			body, err := receiveRestoreMail(conn)
			f.messages <- restoreMail{body, err}
		}
	}()
	return f
}

func receiveRestoreMail(conn net.Conn) (message string, resultErr error) {
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	p := textproto.NewConn(conn)
	if err := p.PrintfLine("220 restore fixture ready"); err != nil {
		return "", err
	}
	for _, step := range [][2]string{
		{"EHLO localhost", "250 restore fixture ready"},
		{"MAIL FROM:<sender@example.test>", "250 OK"},
		{"RCPT TO:<recipient@example.test>", "250 OK"},
		{"DATA", "354 Send message"},
	} {
		line, err := p.ReadLine()
		if err != nil {
			return "", err
		}
		if line != step[0] {
			return "", fmt.Errorf("unexpected SMTP command: %q", line)
		}
		if err := p.PrintfLine("%s", step[1]); err != nil {
			return "", err
		}
	}
	body, err := io.ReadAll(p.DotReader())
	if err != nil {
		return "", err
	}
	if err := p.PrintfLine("250 Queued"); err != nil {
		return "", err
	}
	line, err := p.ReadLine()
	if err != nil {
		return "", err
	}
	if line != "QUIT" {
		return "", fmt.Errorf("unexpected SMTP command: %q", line)
	}
	return string(body), p.PrintfLine("221 Bye")
}

func (f *restoreSMTP) await(t *testing.T, deploymentID int64) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	want := fmt.Sprintf("Deployment #%d succeeded", deploymentID)
	for {
		select {
		case mail := <-f.messages:
			if mail.err != nil {
				t.Fatal(mail.err)
			}
			if strings.Contains(mail.body, want) {
				return
			}
		case <-deadline.C:
			t.Fatal("SMTP fixture did not receive the success notification")
		}
	}
}
