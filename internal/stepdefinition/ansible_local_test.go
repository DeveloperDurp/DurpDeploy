package stepdefinition

import (
	"reflect"
	"testing"
)

func TestAnsibleLocalConnection(t *testing.T) {
	a := Ansible{
		Connection: "local", Inventory: "all: {hosts: {localhost: {}}}",
		Playbook: "- hosts: localhost\n  connection: local\n  tasks: []",
	}
	for _, connection := range []string{"", "ssh", "local"} {
		config := a
		config.Connection = connection
		_, err := config.Targets()
		if (err == nil) != (connection == "local") {
			t.Fatalf("connection=%q: %v", connection, err)
		}
	}
	targets, err := a.Targets()
	if err != nil || !reflect.DeepEqual(targets, []string{"localhost"}) {
		t.Fatalf("targets=%v: %v", targets, err)
	}
	roundtrip, err := DecodeAnsible(a.JSON())
	if err != nil || !reflect.DeepEqual(roundtrip, &a) {
		t.Fatalf("local definition lost configuration: %v", err)
	}
	for name, mutate := range map[string]func(*Ansible){
		"unknown mode":        func(a *Ansible) { a.Connection = "automatic" },
		"credential":          func(a *Ansible) { a.SSHKeyVariable = "KEY" },
		"host keys":           func(a *Ansible) { a.KnownHosts = "unused" },
		"remote host":         func(a *Ansible) { a.Inventory = "all: {hosts: {remote: {}}}" },
		"mixed hosts":         func(a *Ansible) { a.Inventory = "all: {hosts: {localhost: {}, remote: {}}}" },
		"SSH address":         func(a *Ansible) { a.Inventory = "all: {hosts: {localhost: {ansible_host: remote}}}" },
		"connection override": func(a *Ansible) { a.Inventory = "all: {hosts: {localhost: {ansible_connection: ssh}}}" },
		"reserved mapping":    func(a *Ansible) { a.ExtraVars = map[string]string{"ansible_connection": "MODE"} },
	} {
		t.Run(name, func(t *testing.T) {
			config := a
			mutate(&config)
			if _, err := config.Targets(); err == nil {
				t.Fatal("invalid local configuration accepted")
			}
		})
	}
	ssh := testAnsible(t)
	ssh.Connection = "ssh"
	if _, err := ssh.Targets(); err != nil {
		t.Fatal("explicit SSH mode failed:", err)
	}
}
