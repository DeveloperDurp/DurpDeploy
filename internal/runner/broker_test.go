package runner

import (
	"sync"
	"testing"
	"time"
)

func TestLogBrokerConcurrentLifecycle(t *testing.T) {
	broker := NewLogBroker()
	start := make(chan struct{})
	done := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Go(func() {
			<-start
			for iteration := 0; iteration < 100; iteration++ {
				broker.beginDeployment(1)
				client := broker.Subscribe(1)
				broker.Broadcast(1, "synthetic output")
				broker.DeploymentActive(1)
				broker.Unsubscribe(1, client)
				for line := range client {
					if line != "synthetic output" {
						t.Errorf("unexpected broker message %q", line)
					}
				}
				broker.endDeployment(1)
			}
		})
	}
	// An unread subscriber must not block broadcasting or other clients.
	slow := broker.Subscribe(1)
	close(start)
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broker lifecycle blocked")
	}
	broker.Unsubscribe(1, slow)
	if broker.DeploymentActive(1) || len(broker.clients) != 0 ||
		len(broker.active) != 0 {
		t.Fatal("broker retained completed deployments or subscriptions")
	}
	broker.Broadcast(1, "after all disconnects")
}
