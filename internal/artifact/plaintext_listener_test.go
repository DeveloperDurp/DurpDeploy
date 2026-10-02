package artifact

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync/atomic"
)

// Accept HTTP and HTTPS on the same authority to observe forbidden downgrades.
type artifactProtocolListener struct {
	net.Listener
	plaintext *atomic.Bool
}

func (l *artifactProtocolListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		reader := bufio.NewReader(conn)
		first, err := reader.Peek(1)
		if err != nil {
			conn.Close()
			continue
		}
		if first[0] == 22 {
			return artifactBufferedConn{Conn: conn, reader: reader}, nil
		}
		request, err := http.ReadRequest(reader)
		if err == nil {
			l.plaintext.Store(true)
			request.Body.Close()
			if _, err := io.WriteString(
				conn,
				"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n",
			); err != nil {
				conn.Close()
				continue
			}
		}
		conn.Close()
	}
}

type artifactBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c artifactBufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}
