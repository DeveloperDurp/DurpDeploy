// Package testdns provides a local DNS fixture for outbound network tests.
package testdns

import (
	"context"
	"errors"
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// Tests using this resolver are serial: they replace the process-wide resolver,
// not the production address validation or literal-IP dialing implementation.
func Install(t *testing.T, answers func(network string) []net.IP) {
	t.Helper()
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			count, peer, err := server.ReadFrom(buffer)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			var request dnsmessage.Message
			if err := request.Unpack(buffer[:count]); err != nil {
				t.Error(err)
				return
			}
			response := dnsmessage.Message{
				Header: dnsmessage.Header{
					ID:            request.ID,
					Response:      true,
					Authoritative: true,
				},
				Questions: request.Questions,
			}
			for _, question := range request.Questions {
				network := "ip4"
				if question.Type == dnsmessage.TypeAAAA {
					network = "ip6"
				} else if question.Type != dnsmessage.TypeA {
					continue
				}
				for _, ip := range answers(network) {
					var body dnsmessage.ResourceBody
					if network == "ip4" && ip.To4() != nil {
						body = &dnsmessage.AResource{A: [4]byte(ip.To4())}
					} else if network == "ip6" && ip.To4() == nil && ip.To16() != nil {
						body = &dnsmessage.AAAAResource{
							AAAA: [16]byte(ip.To16()),
						}
					} else {
						continue
					}
					response.Answers = append(
						response.Answers,
						dnsmessage.Resource{
							Header: dnsmessage.ResourceHeader{
								Name:  question.Name,
								Type:  question.Type,
								Class: dnsmessage.ClassINET,
							},
							Body: body,
						},
					)
				}
			}
			packet, err := response.Pack()
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := server.WriteTo(packet, peer); err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Error(err)
				}
				return
			}
		}
	}()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(
				ctx,
				"udp",
				server.LocalAddr().String(),
			)
		},
	}
	t.Cleanup(func() {
		net.DefaultResolver = previous
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
}
