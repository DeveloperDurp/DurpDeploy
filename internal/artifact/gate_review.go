package artifact

import (
	"bytes"
	"errors"
	"io"
)

// ParseGateReviewReader retains counts while validating all input JSON.
func ParseGateReviewReader(
	reader io.Reader,
	format string,
) (GateReview, error) {
	s := newReviewJSON(reader)
	var counts GateReview
	var err error
	if format == "terraform" {
		counts, _, err = s.terraform(nil)
	} else {
		counts, err = s.summary()
	}
	if err == nil {
		err = s.finish()
	}
	if err != nil {
		return GateReview{}, errors.Join(ErrGateConfig, err)
	}
	return counts, nil
}

func (s *reviewJSON) summary() (GateReview, error) {
	var counts GateReview
	seen := false
	nulls := make(map[string]bool)
	err := s.object(func(key string) error {
		seen = true
		var target *int64
		switch key {
		case "create":
			target = &counts.Create
		case "update":
			target = &counts.Update
		case "delete":
			target = &counts.Delete
		case "read":
			target = &counts.Read
		default:
			return ErrGateConfig
		}
		if err := s.space(); err != nil {
			return err
		}
		if b, _ := s.peek(); b == 'n' {
			nulls[key] = true
			return s.literal("null")
		}
		nulls[key] = false
		return s.integer(target)
	})
	for _, null := range nulls {
		if null {
			return GateReview{}, ErrGateConfig
		}
	}
	if err == nil && (!seen || counts.Create < 0 || counts.Update < 0 ||
		counts.Delete < 0 || counts.Read < 0) {
		err = ErrGateConfig
	}
	return counts, err
}

func ParseGateReview(raw []byte, format string) (GateReview, error) {
	return ParseGateReviewReader(bytes.NewReader(raw), format)
}
