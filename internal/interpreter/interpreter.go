package interpreter

import "fmt"

const (
	Bash       = "bash"
	PowerShell = "pwsh"
	Python     = "python3"
)

func Validate(value string) (string, error) {
	if value == "" {
		return Bash, nil
	}
	switch value {
	case Bash, PowerShell, Python:
		return value, nil
	default:
		return "", fmt.Errorf("interpreter must be bash, pwsh, or python3")
	}
}

func Extension(value string) string {
	switch value {
	case PowerShell:
		return ".ps1"
	case Python:
		return ".py"
	default:
		return ".sh"
	}
}
