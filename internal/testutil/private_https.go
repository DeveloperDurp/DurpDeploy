package testutil

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PrivateHTTPSServer uses a permitted non-loopback destination for save tests.
func PrivateHTTPSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || ip.To4() == nil || !ip.IsGlobalUnicast() ||
			ip.IsLoopback() {
			continue
		}
		server := httptest.NewUnstartedServer(handler)
		t.Cleanup(server.Close)
		Close(t, server.Listener)
		server.Listener, err = net.Listen(
			"tcp",
			net.JoinHostPort(ip.String(), "0"),
		)
		if err != nil {
			t.Fatal(err)
		}
		server.StartTLS()
		// httptest's trusted certificate includes example.com.
		transport := server.Client().Transport.(*http.Transport)
		transport.TLSClientConfig.ServerName = "example.com"
		return server
	}
	t.Fatal("package save fixture requires a non-loopback IPv4 interface")
	return nil
}
