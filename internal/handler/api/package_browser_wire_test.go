//go:build e2e && packagebrowser

package api_test

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Small test-only CDP transport: no browser automation dependency is installed.
// JSON parameters/results remain at this external protocol boundary.
type packageBrowserWire struct {
	connection net.Conn
	reader     *bufio.Reader
	sequence   int
	events     []packageBrowserEvent
}

type packageBrowserEvent struct {
	Method  string `json:"method"`
	Session string `json:"sessionId"`
}

func connectPackageBrowser(endpoint string) (*packageBrowserWire, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	connection, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		connection.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	if _, err := fmt.Fprintf(
		connection,
		"GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n",
		u.RequestURI(),
		u.Host,
		key,
	); err != nil {
		connection.Close()
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		connection.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		response.Body.Close()
		connection.Close()
		return nil, errors.New("browser websocket upgrade failed")
	}
	return &packageBrowserWire{connection: connection, reader: reader}, nil
}

func (w *packageBrowserWire) writeFrame(opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) <= 65535:
		header = append(
			header,
			0x80|126,
			byte(len(payload)>>8),
			byte(len(payload)),
		)
	default:
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(len(payload)))
	}
	header = append(header, mask[:]...)
	encoded := make([]byte, len(payload))
	for index, value := range payload {
		encoded[index] = value ^ mask[index%4]
	}
	_, err := w.connection.Write(append(header, encoded...))
	return err
}

func (w *packageBrowserWire) readMessage() ([]byte, error) {
	var message []byte
	for {
		var header [2]byte
		if _, err := io.ReadFull(w.reader, header[:]); err != nil {
			return nil, err
		}
		length := uint64(header[1] & 127)
		if length == 126 {
			var size [2]byte
			if _, err := io.ReadFull(w.reader, size[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(size[:]))
		} else if length == 127 {
			var size [8]byte
			if _, err := io.ReadFull(w.reader, size[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(size[:])
		}
		if header[1]&128 != 0 || length > 16<<20 ||
			uint64(len(message))+length > 16<<20 {
			return nil, errors.New("invalid browser websocket frame")
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(w.reader, payload); err != nil {
			return nil, err
		}
		switch header[0] & 15 {
		case 8:
			return nil, io.EOF
		case 9:
			if err := w.writeFrame(10, payload); err != nil {
				return nil, err
			}
			continue
		case 0, 1:
			message = append(message, payload...)
		default:
			return nil, errors.New("unsupported browser websocket frame")
		}
		if header[0]&128 != 0 {
			return message, nil
		}
	}
}

func (w *packageBrowserWire) call(
	method, session string,
	parameters any,
	result any,
) error {
	w.sequence++
	request := struct {
		ID         int    `json:"id"`
		Method     string `json:"method"`
		Session    string `json:"sessionId,omitempty"`
		Parameters any    `json:"params"`
	}{w.sequence, method, session, parameters}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := w.connection.SetDeadline(
		time.Now().Add(30 * time.Second),
	); err != nil {
		return err
	}
	if err := w.writeFrame(1, payload); err != nil {
		return err
	}
	for {
		payload, err := w.readMessage()
		if err != nil {
			return err
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			return err
		}
		if response.ID != w.sequence {
			var event packageBrowserEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				return err
			}
			if event.Method != "" {
				w.events = append(w.events, event)
			}
			continue
		}
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return json.Unmarshal(response.Result, result)
	}
}

func (w *packageBrowserWire) waitEvent(method, session string) error {
	if err := w.connection.SetDeadline(
		time.Now().Add(30 * time.Second),
	); err != nil {
		return err
	}
	for {
		for index, event := range w.events {
			if event.Method == method && event.Session == session {
				w.events = w.events[index+1:]
				return nil
			}
		}
		payload, err := w.readMessage()
		if err != nil {
			return err
		}
		var event packageBrowserEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		w.events = append(w.events, event)
	}
}
