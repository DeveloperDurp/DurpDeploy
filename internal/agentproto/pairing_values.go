package agentproto

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
)

const PairingCodeEntropyBytes = 32

// PairingCode is a 256-bit bootstrap secret. It can only be encoded for the
// short-lived JSON pairing transport and is redacted by String.
type PairingCode struct {
	bytes [PairingCodeEntropyBytes]byte
	valid bool
}

// PairingCodeHash is the durable SHA-256 representation of PairingCode.
type PairingCodeHash [sha256.Size]byte

func ParsePairingCode(raw string) (PairingCode, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != PairingCodeEntropyBytes {
		return PairingCode{}, protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrInvalidPairingCode,
		)
	}

	var code PairingCode
	copy(code.bytes[:], decoded)
	code.valid = true
	return code, nil
}

func (c PairingCode) String() string {
	return "[redacted pairing code]"
}

func (c PairingCode) Hash() PairingCodeHash {
	return sha256.Sum256(c.bytes[:])
}

func (c PairingCode) MarshalJSON() ([]byte, error) {
	if !c.valid {
		return nil, protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrInvalidPairingCode,
		)
	}
	return json.Marshal(base64.RawURLEncoding.EncodeToString(c.bytes[:]))
}

func (c *PairingCode) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrInvalidPairingCode,
		)
	}
	parsed, err := ParsePairingCode(raw)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

func (h PairingCodeHash) matches(code PairingCode) bool {
	actual := code.Hash()
	return subtle.ConstantTimeCompare(h[:], actual[:]) == 1
}

// SHA256Pin is a canonical hexadecimal SHA-256 identity pin.
type SHA256Pin struct {
	digest [sha256.Size]byte
	valid  bool
}

func ParseSHA256Pin(raw string) (SHA256Pin, error) {
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size ||
		hex.EncodeToString(decoded) != raw {
		return SHA256Pin{}, protocolError(
			"pin",
			ReasonInvalid,
			ErrInvalidSHA256Pin,
		)
	}

	var pin SHA256Pin
	copy(pin.digest[:], decoded)
	pin.valid = true
	return pin, nil
}

func (p SHA256Pin) String() string {
	return hex.EncodeToString(p.digest[:])
}

func (p SHA256Pin) matches(other SHA256Pin) bool {
	return p.valid && other.valid &&
		subtle.ConstantTimeCompare(p.digest[:], other.digest[:]) == 1
}

func (p SHA256Pin) MarshalJSON() ([]byte, error) {
	if !p.valid {
		return nil, protocolError(
			"pin",
			ReasonInvalid,
			ErrInvalidSHA256Pin,
		)
	}
	return json.Marshal(p.String())
}

func (p *SHA256Pin) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError(
			"pin",
			ReasonInvalid,
			ErrInvalidSHA256Pin,
		)
	}
	parsed, err := ParseSHA256Pin(raw)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// PullEndpoint is the HTTPS base URL transferred only during bootstrap pairing.
type PullEndpoint struct {
	raw string
}

func ParsePullEndpoint(raw string) (PullEndpoint, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return PullEndpoint{}, protocolError(
			"pull_endpoint",
			ReasonInvalid,
			ErrInvalidPullEndpoint,
		)
	}
	return PullEndpoint{raw: parsed.String()}, nil
}

func (e PullEndpoint) String() string {
	return e.raw
}

func (e PullEndpoint) valid() bool {
	return e.raw != ""
}

func (e PullEndpoint) MarshalJSON() ([]byte, error) {
	if !e.valid() {
		return nil, protocolError(
			"pull_endpoint",
			ReasonInvalid,
			ErrInvalidPullEndpoint,
		)
	}
	return json.Marshal(e.raw)
}

func (e *PullEndpoint) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError(
			"pull_endpoint",
			ReasonInvalid,
			ErrInvalidPullEndpoint,
		)
	}
	parsed, err := ParsePullEndpoint(raw)
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}
