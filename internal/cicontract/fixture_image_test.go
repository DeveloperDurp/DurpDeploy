package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureImagePullsOnlyWhenMissingAndPropagatesFailure(t *testing.T) {
	script, err := filepath.Abs("../../scripts/ensure_fixture_image.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, failedPull := range []string{"0", "1"} {
		t.Run("failedPull="+failedPull, func(t *testing.T) {
			directory := t.TempDir()
			bin := fixtureCommand(t, "engine", `
set -eu
[ "$1" = --host=private ]
shift
printf '%s\n' "$*" >> "$STATE/calls"
case $1 in
  image) [ -e "$STATE/image" ] ;;
  pull)
    [ "$FAIL_PULL" = 0 ] || exit 9
    touch "$STATE/image" ;;
  *) exit 1 ;;
esac
`)
			for attempt := 0; attempt < 2; attempt++ {
				command := exec.Command("bash", script, "fixture:tag",
					filepath.Join(bin, "engine"), "--host=private")
				command.Env = append(os.Environ(), "STATE="+directory,
					"FAIL_PULL="+failedPull)
				output, err := command.CombinedOutput()
				if (err != nil) != (failedPull == "1") {
					t.Fatalf("pull result: %v: %s", err, output)
				}
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if failedPull == "1" {
				want = 2
			}
			if got := strings.Count(
				string(calls),
				"pull fixture:tag",
			); got != want {
				t.Fatalf("pull count=%d want=%d: %s", got, want, calls)
			}
		})
	}
}
