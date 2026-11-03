package stepdefinition

import (
	"crypto/ed25519"
	"crypto/rand"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testAnsible(t *testing.T) Ansible {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return Ansible{
		Playbook:       "- hosts: all\n  tasks:\n    - ansible.builtin.ping:\n",
		Inventory:      "all:\n  children:\n    web:\n      hosts:\n        node1:\n          ansible_host: 192.0.2.1\n          ansible_port: 22\n          ansible_user: deploy\n    database:\n      hosts:\n        node2: {}\n",
		SSHKeyVariable: "SSH_KEY", KnownHosts: "192.0.2.1 " + string(ssh.MarshalAuthorizedKey(key)),
		ExtraVars: map[string]string{
			"message": "DEPLOY_MESSAGE",
		}, Tags: []string{"deploy"}, Limit: []string{"web"},
	}
}

func TestAnsibleDefinition(t *testing.T) {
	a := testAnsible(t)
	targets, err := a.Targets()
	if err != nil || !reflect.DeepEqual(targets, []string{"node1"}) {
		t.Fatalf("targets=%v: %v", targets, err)
	}
	a.Limit = nil
	targets, err = a.Targets()
	if err != nil || !reflect.DeepEqual(targets, []string{"node1", "node2"}) {
		t.Fatalf("targets=%v: %v", targets, err)
	}
	for name, mutate := range map[string]func(*Ansible){
		"invalid YAML":        func(a *Ansible) { a.Playbook = "[broken" },
		"multiple documents":  func(a *Ansible) { a.Playbook += "---\n[]\n" },
		"wrong playbook root": func(a *Ansible) { a.Playbook = "tasks: []" },
		"aliases":             func(a *Ansible) { a.Playbook = "- hosts: &target all\n  name: *target" },
		"duplicate keys":      func(a *Ansible) { a.Playbook = "- hosts: all\n  hosts: other" },
		"oversized":           func(a *Ansible) { a.Inventory = strings.Repeat("a", 256*1024+1) },
		"node budget":         func(a *Ansible) { a.Playbook = "- tasks:\n" + strings.Repeat("    - debug: x\n", 400) },
		"plugin":              func(a *Ansible) { a.Inventory = "plugin: aws_ec2" },
		"templated address": func(a *Ansible) {
			a.Inventory = "all:\n  hosts:\n    node:\n      ansible_host: '{{ lookup(\"env\", \"HOME\") }}'"
		},
		"connection override": func(a *Ansible) { a.Inventory = "all:\n  hosts:\n    node:\n      ansible_connection: local" },
		"option injection":    func(a *Ansible) { a.Tags = []string{"--extra-vars=bad"} },
		"pattern limit":       func(a *Ansible) { a.Limit = []string{"web:!node1"} },
		"unknown limit":       func(a *Ansible) { a.Limit = []string{"absent"} },
		"host group collision": func(a *Ansible) {
			a.Inventory = "all:\n  hosts:\n    web: {}\n  children:\n    web:\n      hosts:\n        other: {}\n"
		},
		"reserved variable":         func(a *Ansible) { a.ExtraVars = map[string]string{"ansible_ssh_common_args": "SECRET"} },
		"invalid host verification": func(a *Ansible) { a.KnownHosts = "not a host key" },
	} {
		t.Run(name, func(t *testing.T) {
			a := testAnsible(t)
			mutate(&a)
			if _, err := a.Targets(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestAnsibleKindBoundaries(t *testing.T) {
	a := testAnsible(t)
	valid := Definition{
		Kind:            AnsibleKind,
		Ansible:         &a,
		ContainerImage:  "example/ansible:1",
		ExecutionTarget: "local",
		NetworkMode:     "bridge",
	}
	if err := valid.ValidateKind(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Definition){
		func(d *Definition) { d.Kind = "unknown" }, func(d *Definition) { d.Kind = Script },
		func(d *Definition) { d.ExecutionTarget = "agent" }, func(d *Definition) { d.AgentSelectors = []string{"ssh"} },
		func(d *Definition) { d.Interpreter = "bash" }, func(d *Definition) { d.ScriptBody = "echo injected" },
		func(d *Definition) { d.VariableNames = []string{"SECRET"} }, func(d *Definition) { d.NetworkMode = "" },
	} {
		d := valid
		mutate(&d)
		if err := d.ValidateKind(); err == nil {
			t.Fatal("invalid kind combination accepted")
		}
	}
	if err := (Definition{}).ValidateKind(); err != nil {
		t.Fatal("legacy script rejected", err)
	}
}
