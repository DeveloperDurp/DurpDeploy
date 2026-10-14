package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/repository"
)

func TestArtifactGateImageID(t *testing.T) {
	for _, mode := range []string{"cached", "pull", "pull-failure", "inspect-failure", "empty"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "docker")
			state := filepath.Join(dir, "pulled")
			script := fmt.Sprintf(`#!/bin/sh
mode=%q
case " $* " in
*" pull "*)
  if [ "$mode" = pull-failure ]; then exit 1; fi
  : > %q
  exit 0
  ;;
esac
if [ "$mode" = inspect-failure ] || [ "$mode" = pull-failure ]; then exit 1; fi
if [ "$mode" = pull ] && [ ! -f %q ]; then exit 1; fi
if [ "$mode" = empty ]; then printf ' \n'; else printf ' sha256:plan-image\n'; fi
`, mode, state, state)
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			runner := &DeploymentRunner{engine: containerEndpoint{
				kind: "docker", binary: binary, url: "unix:///var/run/docker.sock",
			}}
			id, err := runner.artifactGateImageID(
				t.Context(),
				"example/image:1",
			)
			if mode == "cached" || mode == "pull" {
				if err != nil || id != "sha256:plan-image" {
					t.Fatalf("id=%q err=%v", id, err)
				}
			} else if !errors.Is(err, repository.ErrArtifactGate) || id != "" {
				t.Fatalf("id=%q err=%v", id, err)
			}
		})
	}
}
