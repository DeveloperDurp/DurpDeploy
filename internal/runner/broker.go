package runner

import "sync"

type LogBroker struct {
	mu      sync.RWMutex
	clients map[int64]map[chan string]struct{} // deploymentID -> clients
	active  map[int64]int
}

func NewLogBroker() *LogBroker {
	return &LogBroker{
		clients: make(map[int64]map[chan string]struct{}),
		active:  make(map[int64]int),
	}
}

func (b *LogBroker) beginDeployment(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active[id]++
}

func (b *LogBroker) endDeployment(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active[id]--
	if b.active[id] == 0 {
		delete(b.active, id)
	}
}

// DeploymentActive includes cancellation and cleanup, which can emit final
// logs after the deployment's HTTP status has already become terminal.
func (b *LogBroker) DeploymentActive(id int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.active[id] > 0
}

func (b *LogBroker) Subscribe(deploymentID int64) chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	if b.clients[deploymentID] == nil {
		b.clients[deploymentID] = make(map[chan string]struct{})
	}
	b.clients[deploymentID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *LogBroker) Unsubscribe(deploymentID int64, ch chan string) {
	b.mu.Lock()
	if clients, ok := b.clients[deploymentID]; ok {
		delete(clients, ch)
		if len(clients) == 0 {
			delete(b.clients, deploymentID)
		}
	}
	b.mu.Unlock()
	close(ch)
}

func (b *LogBroker) Broadcast(deploymentID int64, line string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if clients, ok := b.clients[deploymentID]; ok {
		for ch := range clients {
			select {
			case ch <- line:
			default:
			}
		}
	}
}
