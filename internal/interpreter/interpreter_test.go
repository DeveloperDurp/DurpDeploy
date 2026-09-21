package interpreter

import "testing"

func TestValidate(t *testing.T) {
	for _, value := range []string{"", Bash, PowerShell, Python} {
		got, err := Validate(value)
		if err != nil {
			t.Fatalf("Validate(%q): %v", value, err)
		}
		if value == "" && got != Bash || value != "" && got != value {
			t.Fatalf("Validate(%q) = %q", value, got)
		}
	}
	if _, err := Validate("/bin/sh"); err == nil {
		t.Fatal("Validate accepted arbitrary interpreter path")
	}
}
