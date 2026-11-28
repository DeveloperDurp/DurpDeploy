package notify

import (
	"errors"
	"testing"
)

func TestValidateRecipients(t *testing.T) {
	for _, raw := range []string{
		"", " ", ", ,", "a@example.com", " a@example.com, b@example.com, ",
		"ops+deploy@example.com", "a@host.containers.internal",
		`"ops"@example.com`, `"ops team"@example.com`,
		`"ops()<>"@example.com`, `"o\"ps"@example.com`,
		`"ops; team"@example.com`, "a@[2001:db8::1]",
	} {
		if err := ValidateRecipients(raw); err != nil {
			t.Errorf("valid recipients %q: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"a@example.com\r\nBcc: b@example.com", "a@example.com\n",
		"\ra@example.com", "\ta@example.com", "a@exam\tple.com",
		"a@example.com\x00", "a@example.com\x7f", "a@example.com\u0085",
		"a", "a@", "Name <a@example.com>", "a@example.com (Name)",
		"<a@example.com>", "a@example.com ()", `"ops"@example.com ()`,
		"ops:a@example.com;", "a@ example.com", `"ops" @example.com`,
	} {
		if err := ValidateRecipients(raw); !errors.Is(
			err,
			ErrInvalidRecipients,
		) {
			t.Errorf("invalid recipients %q: %v", raw, err)
		}
	}
}
