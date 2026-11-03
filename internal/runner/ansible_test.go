package runner

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"

	"durpdeploy/internal/stepdefinition"
)

func TestAnsiblePrivateArchive(t *testing.T) {
	archive, err := ansibleArchive(
		map[string]string{
			"id_key":       "private-key-bytes",
			"vars.json":    "selected-secret",
			"playbook.yml": "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if header.Mode != 0600 || header.Uid != 65534 || header.Gid != 65534 {
			t.Fatal("unsafe staging permissions")
		}
	}
	if count != 3 {
		t.Fatal("missing staged inputs")
	}
	if _, err := ansibleArchive(map[string]string{"vars.json": strings.Repeat("x", ansibleStagingBytes)}); err == nil {
		t.Fatal("unbounded private staging")
	}
}

func TestAnsibleDoesNotExportEffectiveEnvironment(t *testing.T) {
	request := localStepAttempt{
		step: deploymentStep{Kind: stepdefinition.AnsibleKind},
		environment: map[string]string{
			"SELECTED":       "secret",
			"UNSELECTED":     "private",
			"ANSIBLE_CONFIG": "poison",
		},
	}
	environment, args, err := request.containerEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if len(environment) != 0 {
		t.Fatal("Ansible exported effective variables")
	}
	for _, arg := range args {
		for _, private := range []string{"secret", "private", "poison", "UNSELECTED"} {
			if strings.Contains(arg, private) {
				t.Fatal("private environment leaked to arguments")
			}
		}
	}
}
