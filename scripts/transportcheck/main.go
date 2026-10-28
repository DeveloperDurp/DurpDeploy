// Command transportcheck checks only targets recorded by the owned fixture.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var ports = []int{
	80,
	443,
	1433,
	2019,
	2375,
	2376,
	5432,
	6060,
	8080,
	9090,
	23187,
}

type target struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type finding struct {
	Target string `json:"target"`
	Check  string `json:"check"`
	Pass   bool   `json:"pass"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected probes or listeners")
		os.Exit(2)
	}
	var findings []finding
	var err error
	switch os.Args[1] {
	case "probes":
		findings, err = probes(os.Stdin)
	case "listeners":
		findings, err = listeners(os.Stdin)
	default:
		err = errors.New("invalid check mode")
	}
	if err != nil {
		// Inputs and runtime error messages are private and may contain secrets.
		fmt.Fprintln(os.Stderr, "transport evidence invalid or incomplete")
		os.Exit(2)
	}
	if json.NewEncoder(os.Stdout).Encode(findings) != nil {
		os.Exit(2)
	}
	for _, f := range findings {
		if !f.Pass {
			os.Exit(1)
		}
	}
}

func decode(input io.Reader, value any) error {
	const maxEvidenceSize = 1 << 20
	data, err := io.ReadAll(io.LimitReader(input, maxEvidenceSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxEvidenceSize {
		return errors.New("evidence exceeds input limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

func probes(input io.Reader) ([]finding, error) {
	var targets []target
	if err := decode(input, &targets); err != nil {
		return nil, err
	}
	if len(targets) != 2 || targets[0].Name != "app" ||
		targets[1].Name != "caddy" {
		return nil, errors.New("missing expected targets")
	}
	var result []finding
	for _, target := range targets {
		addr, err := netip.ParseAddr(target.IP)
		// ponytail: IPv4 targets; add an IPv6 fixture before active IPv6 probes.
		if err != nil || !addr.IsPrivate() || !addr.Is4() {
			return nil, errors.New("nonfixture target")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		for _, port := range ports {
			dialer := net.Dialer{Timeout: time.Second}
			conn, dialErr := dialer.DialContext(ctx, "tcp",
				net.JoinHostPort(target.IP, strconv.Itoa(port)))
			open := dialErr == nil
			if open {
				if err := conn.Close(); err != nil {
					cancel()
					return nil, errors.New("probe cleanup failed")
				}
			} else if !errors.Is(dialErr, syscall.ECONNREFUSED) {
				var timeout net.Error
				if !errors.As(dialErr, &timeout) || !timeout.Timeout() {
					cancel()
					return nil, errors.New("probe incomplete")
				}
			}
			want := (target.Name == "app" && (port == 8080 || port == 23187)) ||
				(target.Name == "caddy" && (port == 80 || port == 443))
			result = append(result, finding{target.Name,
				"tcp-" + strconv.Itoa(port), open == want})
		}
		cancel()
	}
	return result, nil
}

type listenerTable struct {
	Target   string `json:"target"`
	Protocol string `json:"protocol"`
	Data     string `json:"data"`
}

func listeners(input io.Reader) ([]finding, error) {
	var tables []listenerTable
	if err := decode(input, &tables); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var result []finding
	for _, table := range tables {
		key := table.Target + "/" + table.Protocol
		if (table.Target != "app" && table.Target != "caddy") || seen[key] ||
			(table.Protocol != "tcp" && table.Protocol != "tcp6" &&
				table.Protocol != "udp" && table.Protocol != "udp6") {
			return nil, errors.New("invalid listener table")
		}
		seen[key] = true
		lines := strings.Split(strings.TrimSpace(table.Data), "\n")
		if len(lines) == 0 || !strings.Contains(lines[0], "local_address") {
			return nil, errors.New("missing listener data")
		}
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				return nil, errors.New("malformed listener data")
			}
			if strings.HasPrefix(table.Protocol, "tcp") && fields[3] != "0A" {
				continue
			}
			if strings.HasPrefix(table.Protocol, "udp") &&
				strings.Trim(fields[2], "0:") != "" {
				continue // Connected client sockets are not listeners.
			}
			pass, port, err := allowedListener(
				table.Target,
				table.Protocol,
				fields[1],
			)
			if err != nil {
				return nil, err
			}
			result = append(result, finding{table.Target,
				table.Protocol + "-" + strconv.Itoa(port), pass})
		}
	}
	if len(seen) != 8 || len(result) == 0 {
		return nil, errors.New("incomplete listener inventory")
	}
	return result, nil
}

func allowedListener(service, protocol, local string) (bool, int, error) {
	address, portText, ok := strings.Cut(local, ":")
	port, err := strconv.ParseUint(portText, 16, 16)
	if !ok || err != nil || (len(address) != 8 && len(address) != 32) {
		return false, 0, errors.New("invalid socket address")
	}
	// Docker's embedded DNS uses this exact loopback address and random ports.
	if address == "0B00007F" {
		return true, int(port), nil
	}
	tcp := strings.HasPrefix(protocol, "tcp")
	allowed := service == "app" && tcp && (port == 8080 || port == 23187)
	if service == "caddy" {
		loopback := address == "0100007F" ||
			address == "00000000000000000000000001000000"
		allowed = (tcp && port == 80) || port == 443 ||
			(tcp && port == 2019 && loopback)
	}
	return allowed, int(port), nil
}
