package agentproto

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestSelector_Parse_canonicalizes_exact_matches(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "canonical region=us,role=web",
			input: " role = web , region = us ",
			want:  "region=us,role=web",
		},
		{
			name:  "preserves value case",
			input: "region=US,role=Web",
			want:  "region=US,role=Web",
		},
		{
			name:  "allows no required tags",
			input: "",
			want:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			input := test.input

			// When
			selector, err := ParseSelector(input)

			// Then
			if err != nil {
				t.Fatalf("ParseSelector(%q): %v", input, err)
			}
			if got := selector.String(); got != test.want {
				t.Fatalf("selector.String() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSelector_Parse_rejects_invalid_input(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "duplicate key after trimming",
			input:   "region=us, region = eu",
			wantErr: ErrDuplicateSelectorKey,
		},
		{
			name:    "empty pair",
			input:   "region=us,",
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "missing value",
			input:   "region=",
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "uppercase key",
			input:   "Region=us",
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "key longer than 32 bytes",
			input:   strings.Repeat("a", 33) + "=us",
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "value longer than 64 bytes",
			input:   "region=" + strings.Repeat("a", 65),
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "invalid UTF-8 value",
			input:   "region=" + string([]byte{0xff}),
			wantErr: ErrInvalidSelector,
		},
		{
			name:    "more than 32 keys",
			input:   selectorInput(33),
			wantErr: ErrSelectorLimit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			input := test.input

			// When
			_, err := ParseSelector(input)

			// Then
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"ParseSelector(%q) error = %v, want %v",
					input,
					err,
					test.wantErr,
				)
			}
		})
	}
}

func selectorInput(count int) string {
	pairs := make([]string, count)
	for index := range pairs {
		pairs[index] = "key" + strconv.Itoa(index) + "=value"
	}
	return strings.Join(pairs, ",")
}
