package notify

import (
	"context"
	"net"
	"testing"
	"time"

	"durpdeploy/internal/events"
)

func TestEmailNotifier_StopsSilentPeer(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline],
			func(t *testing.T) {
				// Given: an SMTP peer accepts but never sends its greeting.
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { listener.Close() })
				accepted := make(chan net.Conn, 1)
				go func() {
					conn, err := listener.Accept()
					if err == nil {
						accepted <- conn
					}
				}()
				host, port, err := net.SplitHostPort(listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				notifier := NewEmailNotifier(SMTPConfig{Host: host, Port: port})
				ctx, cancel := context.WithCancel(t.Context())
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(
						t.Context(),
						200*time.Millisecond,
					)
				}
				defer cancel()
				result := make(chan error, 1)
				go func() {
					_, err := notifier.Notify(ctx, events.Event{
						NotifyEmails: []string{"recipient@example.com"},
					})
					result <- err
				}()
				var conn net.Conn
				select {
				case conn = <-accepted:
					defer conn.Close()
				case <-time.After(2 * time.Second):
					t.Fatal("SMTP connection was not accepted")
				}
				// When: the caller cancels or its deadline expires.
				if !deadline {
					cancel()
				}
				// Then: delivery returns without waiting for the SMTP peer.
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("silent SMTP peer unexpectedly succeeded")
					}
				case <-time.After(time.Second):
					conn.Close()
					<-result
					t.Fatal("SMTP delivery ignored cancellation")
				}
			})
	}
}
