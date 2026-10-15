// Package roles defines the shared permission order.
package roles

func Rank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "deployer":
		return 2
	case "maintainer":
		return 3
	case "admin":
		return 4
	default:
		return 0
	}
}

func Valid(role string) bool { return Rank(role) != 0 }

func AtLeast(role, required string) bool {
	return Valid(required) && Rank(role) >= Rank(required)
}

func All() []string {
	return []string{"viewer", "deployer", "maintainer", "admin"}
}
