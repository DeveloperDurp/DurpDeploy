package stepdefinition

import (
	"reflect"
	"testing"
)

func TestAnsibleImplicitInventoryGroups(t *testing.T) {
	for _, scenario := range []struct {
		name, inventory, limit string
		want                   []string
	}{
		{"implicit all", "web: {hosts: {node2: {}}}", "all", []string{"node2"}},
		{"sibling roots", "all: {hosts: {node1: {}}}\nweb: {hosts: {node2: {}}}", "all", []string{"node1", "node2"}},
		{"implicit ungrouped", "all: {hosts: {node1: {}}}\nweb: {hosts: {node2: {}}}", "ungrouped", []string{"node1"}},
		{"remove grouped hosts", "ungrouped: {hosts: {node1: {}, node3: {}}}\nweb: {hosts: {node1: {}, node2: {}}}", "ungrouped", []string{"node3"}},
		{"empty ungrouped", "ungrouped: {hosts: {node1: {}}}\nweb: {hosts: {node1: {}}}", "ungrouped", nil},
		{"nested all", "web: {children: {all: {hosts: {node1: {}}}}}", "all", nil},
		{"ungrouped children", "ungrouped: {children: {web: {hosts: {node1: {}}}}}", "all", nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			a := testAnsible(t)
			a.Inventory = scenario.inventory
			a.Limit = []string{scenario.limit}
			got, err := a.Targets()
			if scenario.want == nil {
				if err == nil {
					t.Fatal("invalid or empty inventory selection accepted")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, scenario.want) {
				t.Fatalf("targets=%v want=%v: %v", got, scenario.want, err)
			}
		})
	}
}

func TestAnsibleInventoryBooleanStrings(t *testing.T) {
	for _, value := range []string{"yes", "Yes", "YES", "no", "No", "NO", "on", "On", "ON", "off", "Off", "OFF"} {
		for _, inventory := range []string{
			"all: {hosts: {" + value + ": {}}}",
			value + ": {hosts: {node: {}}}",
			"all: {hosts: {node: {ansible_host: " + value + "}}}",
			"all: {hosts: {node: {ansible_user: " + value + "}}}",
		} {
			t.Run(inventory, func(t *testing.T) {
				a := testAnsible(t)
				a.Limit = nil
				a.Inventory = inventory
				if _, err := a.Targets(); err == nil {
					t.Fatal("ambiguous YAML 1.1 string accepted")
				}
			})
		}
	}
	a := testAnsible(t)
	a.Inventory = `"on": {hosts: {"yes": {ansible_host: "no", ansible_user: "off"}}}`
	a.Limit = []string{"on"}
	if targets, err := a.Targets(); err != nil ||
		!reflect.DeepEqual(targets, []string{"yes"}) {
		t.Fatalf("quoted inventory strings rejected: %v, %v", targets, err)
	}
}
