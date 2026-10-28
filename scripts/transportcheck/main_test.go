package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func listenerFixture() []listenerTable {
	var tables []listenerTable
	for _, service := range []string{"app", "caddy"} {
		for _, protocol := range []string{"tcp", "tcp6", "udp", "udp6"} {
			tables = append(tables, listenerTable{service, protocol,
				"sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"})
		}
	}
	tables[0].Data += "0: 00000000:1F90 00000000:0000 0A 0 0 0 0 0 1\n"
	tables[4].Data += "0: 00000000:01BB 00000000:0000 0A 0 0 0 0 0 1\n"
	return tables
}

func TestListenerInventoryRejectsUnexpectedAndMissingEvidence(t *testing.T) {
	for _, mode := range []string{"complete", "unexpected-port", "missing-table", "invalid-data"} {
		t.Run(mode, func(t *testing.T) {
			tables := listenerFixture()
			switch mode {
			case "unexpected-port":
				tables[0].Data += "1: 00000000:F123 00000000:0000 0A 0 0 0 0 0 1\n"
			case "missing-table":
				tables = tables[:7]
			case "invalid-data":
				tables[0].Data += "private-canary-invalid-line\n"
			}
			data, err := json.Marshal(tables)
			if err != nil {
				t.Fatal(err)
			}
			findings, err := listeners(strings.NewReader(string(data)))
			pass := err == nil
			for _, f := range findings {
				pass = pass && f.Pass
			}
			if pass != (mode == "complete") {
				t.Fatal(
					"listener gate accepted incomplete or unexpected evidence",
				)
			}
		})
	}
}

func TestProbeRejectsNonFixtureTargetsBeforeNetworkAccess(t *testing.T) {
	for _, input := range []string{
		`[]`,
		`[{"name":"app","ip":"203.0.113.1"},{"name":"caddy","ip":"10.0.0.1"}]`,
		`[{"name":"app","ip":"127.0.0.1"},{"name":"caddy","ip":"10.0.0.1"}]`,
		`[{"name":"private-canary","ip":"10.0.0.1"}]`,
		`[] {}`, `{"extra":"private-canary"}`,
	} {
		if _, err := probes(strings.NewReader(input)); err == nil {
			t.Fatal("probe admitted incomplete or nonfixture target evidence")
		}
	}
}

func TestCaddyAdministrationMustRemainLoopback(t *testing.T) {
	for _, socket := range []string{"00000000:07E3", "0100007F:07E3"} {
		pass, _, err := allowedListener("caddy", "tcp", socket)
		if err != nil || pass != strings.HasPrefix(socket, "0100007F:") {
			t.Fatal("public Caddy administration was permitted")
		}
	}
}

func TestDecodeRejectsOversizedEvidence(t *testing.T) {
	input := `[]` + strings.Repeat(" ", 1<<20) + `{"unexpected":true}`
	var tables []listenerTable
	if err := decode(strings.NewReader(input), &tables); err == nil {
		t.Fatal("accepted evidence beyond the input limit")
	}
}
