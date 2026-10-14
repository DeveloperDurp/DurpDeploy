package artifact

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// reviewJSON validates skipped values without retaining large strings or numbers.
// encoding/json.Decoder buffers a complete value even when its fields are ignored.
type reviewJSON struct {
	reader    *bufio.Reader
	output    *bytes.Buffer
	limit     int
	truncated bool
	depth     int
}

func newReviewJSON(reader io.Reader) *reviewJSON {
	return &reviewJSON{reader: bufio.NewReader(reader)}
}

func (s *reviewJSON) read() (byte, error) {
	b, err := s.reader.ReadByte()
	if err == nil && s.output != nil {
		if s.limit == 0 || s.output.Len() < s.limit {
			s.output.WriteByte(b)
		} else {
			s.truncated = true
		}
	}
	return b, err
}

func (s *reviewJSON) peek() (byte, error) {
	p, err := s.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return p[0], nil
}

func (s *reviewJSON) space() error {
	for {
		b, err := s.peek()
		if err != nil || (b != ' ' && b != '\t' && b != '\r' && b != '\n') {
			return err
		}
		if _, err := s.read(); err != nil {
			return err
		}
	}
}

func (s *reviewJSON) expect(want byte) error {
	b, err := s.read()
	if err != nil {
		return err
	}
	if b != want {
		return ErrGateConfig
	}
	return nil
}

func (s *reviewJSON) literal(word string) error {
	for i := range len(word) {
		if err := s.expect(word[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *reviewJSON) enter() error {
	s.depth++
	// Match encoding/json's existing nesting ceiling, independent of byte size.
	if s.depth > 10000 {
		return ErrGateConfig
	}
	return nil
}

func (s *reviewJSON) skip() error {
	if err := s.space(); err != nil {
		return err
	}
	b, err := s.peek()
	if err != nil {
		return err
	}
	switch b {
	case '{':
		return s.object(nil)
	case '[':
		return s.array(s.skip)
	case '"':
		_, _, err := s.stringToken(0)
		return err
	case 't':
		return s.literal("true")
	case 'f':
		return s.literal("false")
	case 'n':
		return s.literal("null")
	default:
		return s.number()
	}
}

func (s *reviewJSON) decode(target any) error {
	var raw bytes.Buffer
	s.output = &raw
	err := s.skip()
	s.output = nil
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(&raw)
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return ErrGateConfig
	}
	return nil
}

func (s *reviewJSON) integer(target *int64) error {
	var raw bytes.Buffer
	s.output, s.limit = &raw, 32
	s.truncated = false
	err := s.skip()
	s.output, s.limit = nil, 0
	if err != nil {
		return err
	}
	if s.truncated {
		return ErrGateConfig
	}
	if err := json.Unmarshal(raw.Bytes(), target); err != nil {
		return ErrGateConfig
	}
	return nil
}

func (s *reviewJSON) finish() error {
	err := s.space()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrGateConfig
}
