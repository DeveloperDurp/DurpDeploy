package interpreter

import "testing"

func TestValidate(t *testing.T) {
	tests := map[string]string{
		"":           Bash,
		Bash:         Bash,
		PowerShell:   PowerShell,
		"powershell": PowerShell,
		Python:       Python,
	}
	for value, want := range tests {
		got, err := Validate(value)
		if err != nil {
			t.Fatalf("Validate(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("Validate(%q) = %q, want %q", value, got, want)
		}
	}
	if _, err := Validate("/bin/sh"); err == nil {
		t.Fatal("Validate accepted arbitrary interpreter path")
	}
}
