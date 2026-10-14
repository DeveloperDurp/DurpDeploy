package artifact

import (
	"encoding/json"
	"io"
)

// A bounded prefix recognizes review names and versions. Truncation discards
// only unused text, never the validation of the rest of the string.
func (s *reviewJSON) stringToken(limit int) (string, bool, error) {
	if err := s.expect('"'); err != nil {
		return "", false, err
	}
	var raw []byte
	if limit > 0 {
		raw = append(raw, '"')
	}
	truncated := false
	for {
		token, n, err := s.stringAtom()
		if err != nil {
			return "", false, err
		}
		if n == 0 {
			break
		}
		if limit > 0 && !truncated {
			if len(raw)+n > limit {
				truncated = true
			} else {
				raw = append(raw, token[:n]...)
			}
		}
	}
	if limit == 0 {
		return "", false, nil
	}
	raw = append(raw, '"')
	var value string
	err := json.Unmarshal(raw, &value)
	return value, truncated, err
}

func jsonDigit(b byte) bool { return b >= '0' && b <= '9' }

func (s *reviewJSON) digits() error {
	b, err := s.peek()
	if err != nil {
		return err
	}
	if !jsonDigit(b) {
		return ErrGateConfig
	}
	for {
		if _, err := s.read(); err != nil {
			return err
		}
		b, err = s.peek()
		if err != nil || !jsonDigit(b) {
			if err == nil || err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *reviewJSON) number() error {
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b == '-' {
		if err := s.expect('-'); err != nil {
			return err
		}
		b, err = s.peek()
		if err != nil {
			return err
		}
	}
	if b == '0' {
		err = s.expect('0')
	} else {
		err = s.digits()
	}
	if err != nil {
		return err
	}
	return s.numberTail()
}

func (s *reviewJSON) numberTail() error {
	b, err := s.peek()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if b == '.' {
		if err := s.expect('.'); err != nil {
			return err
		}
		if err := s.digits(); err != nil {
			return err
		}
	}
	return s.exponent()
}

func (s *reviewJSON) exponent() error {
	b, err := s.peek()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if b != 'e' && b != 'E' {
		return nil
	}
	if _, err := s.read(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == '+' || b == '-' {
		if _, err := s.read(); err != nil {
			return err
		}
	}
	return s.digits()
}

func (s *reviewJSON) stringAtom() ([6]byte, int, error) {
	var token [6]byte
	b, err := s.read()
	if err != nil {
		return token, 0, err
	}
	if b == '"' {
		return token, 0, nil
	}
	if b < 0x20 {
		return token, 0, ErrGateConfig
	}
	token[0] = b
	if b != '\\' {
		return token, 1, nil
	}
	token[1], err = s.read()
	if err != nil {
		return token, 0, err
	}
	switch token[1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return token, 2, nil
	case 'u':
		if err := s.unicodeEscape(&token); err != nil {
			return token, 0, err
		}
		return token, 6, nil
	default:
		return token, 0, ErrGateConfig
	}
}

func (s *reviewJSON) unicodeEscape(token *[6]byte) error {
	for i := 2; i < len(token); i++ {
		b, err := s.read()
		if err != nil {
			return err
		}
		if !jsonDigit(b) && (b < 'a' || b > 'f') && (b < 'A' || b > 'F') {
			return ErrGateConfig
		}
		token[i] = b
	}
	return nil
}
