package runner

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (p containerEndpoint) hostname(ctx context.Context) string {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := p.command(checkCtx, "info", "--format", "{{json .}}").Output()
	if err != nil {
		return "unknown"
	}
	var info struct {
		Name string
		Host struct{ Hostname string }
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return "unknown"
	}
	host := info.Name
	if p.kind == "podman" {
		host = info.Host.Hostname
	}
	if strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return host
}
