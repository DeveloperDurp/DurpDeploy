package stepdefinition

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"
)

const Script = "script"
const AnsibleKind = "ansible"

var ErrInvalidAnsible = errors.New("invalid Ansible configuration")

// Ansible contains configuration and variable references, never credentials.
// swagger:model Ansible
type Ansible struct {
	Playbook       string            `json:"playbook"`
	Inventory      string            `json:"inventory"`
	ExtraVars      map[string]string `json:"extra_vars"`
	SSHKeyVariable string            `json:"ssh_key_variable"`
	KnownHosts     string            `json:"known_hosts"`
	Tags           []string          `json:"tags"`
	Limit          []string          `json:"limit"`
	// Check decodes historical step snapshots. New writes use run options.
	// swagger:ignore
	Check *bool `json:"check,omitempty"`
}

var ansibleIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var inventoryIdentifier = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func (d Definition) StepKind() string {
	if d.Kind == "" {
		return Script
	}
	return d.Kind
}

func (a *Ansible) JSON() string {
	if a == nil {
		return ""
	}
	value, _ := json.Marshal(a)
	return string(value)
}

func DecodeAnsible(raw string) (*Ansible, error) {
	if raw == "" {
		return nil, nil
	}
	var result Ansible
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, errors.New("invalid Ansible configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid Ansible configuration")
	}
	return &result, nil
}

// ValidateKind applies at writes, snapshot admission, and execution.
func (d Definition) ValidateKind() error {
	switch d.StepKind() {
	case Script:
		if d.Ansible != nil {
			return errors.New(
				"script steps cannot contain Ansible configuration",
			)
		}
		return nil
	case AnsibleKind:
		return d.validateAnsibleKind()
	default:
		return errors.New("unknown step kind")
	}
}

func (d Definition) ValidateWrite() error {
	if d.Ansible != nil && d.Ansible.Check != nil {
		return errors.New(
			"select Ansible check mode when starting an execution",
		)
	}
	return d.ValidateKind()
}

func (d Definition) validateAnsibleKind() error {
	if d.Ansible == nil {
		return errors.New("Ansible configuration is required")
	}
	if d.ScriptBody != "" || d.Interpreter != "" ||
		len(d.VariableNames) != 0 {
		return errors.New("Ansible steps cannot contain script fields")
	}
	if d.ExecutionTarget != "" && d.ExecutionTarget != "local" ||
		len(d.AgentSelectors) != 0 ||
		d.AgentExecutionMode != "" && d.AgentExecutionMode != "host" {
		return errors.New(
			"Ansible requires a server container; remote agents are unsupported",
		)
	}
	if strings.TrimSpace(d.ContainerImage) == "" {
		return errors.New("Ansible execution image is required")
	}
	if d.NetworkMode != "none" && d.NetworkMode != "bridge" {
		return errors.New(
			"Ansible requires an explicit none or bridge network selection",
		)
	}
	_, err := d.Ansible.Targets()
	return err
}

func yamlDocument(
	raw, label string,
	kind yaml.Kind,
	budget *int,
) (*yaml.Node, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return nil, fmt.Errorf("%s must contain 1 to 262144 bytes", label)
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid %s YAML", label)
	}
	if decoder.Decode(new(yaml.Node)) != io.EOF {
		return nil, fmt.Errorf("%s requires one YAML document", label)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != kind {
		return nil, fmt.Errorf("invalid %s structure", label)
	}
	if err := validateYAML(&doc, budget); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return doc.Content[0], nil
}

func validateYAML(n *yaml.Node, budget *int) error {
	*budget--
	if *budget < 0 {
		return errors.New("YAML exceeds 1024 combined nodes")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return errors.New("YAML aliases and anchors are unsupported")
	}
	switch n.Tag {
	case "",
		"!!map",
		"!!seq",
		"!!str",
		"!!int",
		"!!bool",
		"!!null",
		"!!float",
		"!!timestamp":
	default:
		return errors.New("custom YAML tags are unsupported")
	}
	keys := map[string]bool{}
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			if child.Kind != yaml.ScalarNode || child.Tag != "!!str" ||
				keys[child.Value] {
				return errors.New("YAML requires unique string keys")
			}
			keys[child.Value] = true
		}
		if err := validateYAML(child, budget); err != nil {
			return err
		}
	}
	return nil
}

func (a Ansible) Targets() ([]string, error) {
	budget := 1024
	if _, err := yamlDocument(a.Playbook, "playbook", yaml.SequenceNode, &budget); err != nil {
		return nil, err
	}
	inventory, err := yamlDocument(
		a.Inventory,
		"inventory",
		yaml.MappingNode,
		&budget,
	)
	if err != nil {
		return nil, err
	}
	if err := validateInventoryScalars(inventory); err != nil {
		return nil, err
	}
	if err := a.validateOptions(); err != nil {
		return nil, err
	}
	groups, hosts := map[string][]string{}, map[string]bool{}
	if err := inventoryGroups(inventory, groups, hosts, nil); err != nil {
		return nil, err
	}
	if err := validateInventoryNames(groups, hosts); err != nil {
		return nil, err
	}
	reconcileInventoryGroups(groups, hosts)
	return a.selectTargets(groups, hosts)
}

func validateInventoryScalars(n *yaml.Node) error {
	// Ansible uses YAML 1.1 booleans; yaml.v3 treats these as strings.
	if n.Tag == "!!str" && n.Style == 0 &&
		slices.Contains(
			[]string{"yes", "no", "on", "off"},
			strings.ToLower(n.Value),
		) {
		return errors.New("quote inventory strings yes, no, on, and off")
	}
	for _, child := range n.Content {
		if err := validateInventoryScalars(child); err != nil {
			return err
		}
	}
	return nil
}
func (a Ansible) validateOptions() error {
	if len(a.ExtraVars) > 128 || len(a.Tags) > 64 || len(a.Limit) > 1000 {
		return errors.New("too many Ansible mappings, tags, or limits")
	}
	for name, source := range a.ExtraVars {
		if !ansibleIdentifier.MatchString(name) ||
			reservedAnsibleVariable(name) ||
			!ansibleIdentifier.MatchString(source) {
			return errors.New("invalid Ansible variable mapping")
		}
	}
	if !ansibleIdentifier.MatchString(a.SSHKeyVariable) {
		return errors.New("SSH credential reference is required")
	}
	if err := validateKnownHosts(a.KnownHosts); err != nil {
		return err
	}
	for _, tag := range a.Tags {
		if !inventoryIdentifier.MatchString(tag) || len(tag) > 128 {
			return errors.New("invalid Ansible tag")
		}
	}
	return nil
}
func validateKnownHosts(raw string) error {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return errors.New("known_hosts must contain 1 to 262144 bytes")
	}
	rest := []byte(raw)
	keys := 0
	for len(rest) > 0 {
		_, _, _, _, remaining, parseErr := ssh.ParseKnownHosts(rest)
		if errors.Is(parseErr, io.EOF) {
			break
		}
		if parseErr != nil {
			return errors.New("invalid known_hosts configuration")
		}
		rest = remaining
		keys++
	}
	if keys == 0 {
		return errors.New("known_hosts requires a host key")
	}
	return nil
}

func validateInventoryNames(
	groups map[string][]string,
	hosts map[string]bool,
) error {
	if len(hosts) == 0 || len(hosts) > 1000 {
		return errors.New("inventory requires 1 to 1000 hosts")
	}
	for host := range hosts {
		if _, group := groups[host]; group || host == "all" ||
			host == "ungrouped" {
			return errors.New(
				"inventory host and group names must be distinct",
			)
		}
	}
	return nil
}

func reconcileInventoryGroups(
	groups map[string][]string,
	hosts map[string]bool,
) {
	// Ansible reconciles these groups after parsing every inventory root.
	// Every host belongs to all; only hosts without another group are ungrouped.
	grouped := map[string]bool{}
	for name, members := range groups {
		if name != "all" && name != "ungrouped" {
			for _, host := range members {
				grouped[host] = true
			}
		}
	}
	groups["all"], groups["ungrouped"] = nil, nil
	for host := range hosts {
		groups["all"] = append(groups["all"], host)
		if !grouped[host] {
			groups["ungrouped"] = append(groups["ungrouped"], host)
		}
	}
}

func (a Ansible) selectTargets(
	groups map[string][]string,
	hosts map[string]bool,
) ([]string, error) {
	selected := map[string]bool{}
	if len(a.Limit) == 0 {
		return slices.Sorted(maps.Keys(hosts)), nil
	}
	for _, limit := range a.Limit {
		if !inventoryIdentifier.MatchString(limit) {
			return nil, errors.New("limit must name inventory hosts or groups")
		}
		if hosts[limit] {
			selected[limit] = true
			continue
		}
		members, ok := groups[limit]
		if !ok {
			return nil, errors.New("limit contains an unknown host or group")
		}
		for _, host := range members {
			selected[host] = true
		}
	}
	result := slices.Sorted(maps.Keys(selected))
	if len(result) == 0 {
		return nil, errors.New("limit selects no hosts")
	}
	return result, nil
}

func reservedAnsibleVariable(name string) bool {
	if slices.Contains(
		strings.Fields(
			"as assert await break continue del elif except finally from global nonlocal raise try while with yield connection remote_user become become_user become_method become_flags become_password delegate_to delegate_facts collections module_defaults strategy serial order force_handlers any_errors_fatal max_fail_percentage gather_facts gather_subset gather_timeout fact_path port no_log timeout ignore_errors ignore_unreachable run_once check_mode diff hosts tasks name tags roles vars_files inventory_dir inventory_file playbook_dir play_hosts role_name role_path",
		),
		name,
	) {
		return true
	}
	if strings.HasPrefix(strings.ToLower(name), "ansible_") {
		return true
	}
	switch name {
	case "hostvars",
		"groups",
		"group_names",
		"inventory_hostname",
		"inventory_hostname_short",
		"environment",
		"lookup",
		"query",
		"q",
		"now",
		"undef",
		"omit",
		"vars",
		"True",
		"False",
		"None",
		"async",
		"lambda",
		"class",
		"import",
		"for",
		"if",
		"else",
		"def",
		"return",
		"pass",
		"in",
		"is",
		"not",
		"and",
		"or":
		return true
	}
	return false
}

func inventoryGroups(
	n *yaml.Node,
	groups map[string][]string,
	hosts map[string]bool,
	ancestors []string,
) error {
	if n.Kind != yaml.MappingNode {
		return errors.New("inventory groups must be mappings")
	}
	for i := 0; i < len(n.Content); i += 2 {
		name, body := n.Content[i].Value, n.Content[i+1]
		if len(ancestors) != 0 && (name == "all" || name == "ungrouped") {
			return errors.New(
				"all and ungrouped must be top-level inventory groups",
			)
		}
		if !inventoryIdentifier.MatchString(name) ||
			slices.Contains(ancestors, name) {
			return errors.New("invalid or cyclic inventory group")
		}
		if _, exists := groups[name]; exists {
			return errors.New("duplicate inventory group")
		}
		groups[name] = nil
		path := append(slices.Clone(ancestors), name)
		if err := inventoryGroupBody(body, groups, hosts, path); err != nil {
			return err
		}
	}
	return nil
}

func inventoryGroupBody(
	body *yaml.Node,
	groups map[string][]string,
	hosts map[string]bool,
	path []string,
) error {
	if body.Kind != yaml.MappingNode {
		return errors.New("inventory group requires hosts or children")
	}
	for i := 0; i < len(body.Content); i += 2 {
		field, value := body.Content[i].Value, body.Content[i+1]
		switch field {
		case "hosts":
			if err := inventoryHosts(value, groups, hosts, path); err != nil {
				return err
			}
		case "children":
			if path[len(path)-1] == "ungrouped" {
				return errors.New("ungrouped cannot contain child groups")
			}
			if err := inventoryGroups(value, groups, hosts, path); err != nil {
				return err
			}
		default:
			return errors.New(
				"inventory supports only static hosts and children",
			)
		}
	}
	return nil
}

func inventoryHosts(
	value *yaml.Node,
	groups map[string][]string,
	hosts map[string]bool,
	path []string,
) error {
	if value.Kind != yaml.MappingNode {
		return errors.New("inventory hosts must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		host := value.Content[i].Value
		if !inventoryIdentifier.MatchString(host) || host == "localhost" {
			return errors.New("invalid inventory host")
		}
		if err := inventoryHostValues(value.Content[i+1]); err != nil {
			return err
		}
		hosts[host] = true
		for _, group := range path {
			groups[group] = append(groups[group], host)
		}
	}
	return nil
}

func inventoryHostValues(n *yaml.Node) error {
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return errors.New("host configuration must be a mapping")
	}
	for i := 0; i < len(n.Content); i += 2 {
		if err := validateInventoryHostValue(n.Content[i].Value, n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}
func validateInventoryHostValue(key string, value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return errors.New("host values must be literal")
	}
	switch key {
	case "ansible_host", "ansible_user":
		if value.Tag != "!!str" {
			return errors.New("host address and user must be strings")
		}
		if !inventoryIdentifier.MatchString(value.Value) &&
			(key != "ansible_host" || net.ParseIP(value.Value) == nil) {
			return errors.New(
				"host address and user must be literal names or IP addresses",
			)
		}
	case "ansible_port":
		port, err := strconv.Atoi(value.Value)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("invalid inventory port")
		}
	default:
		return errors.New("unsupported inventory host option")
	}
	return nil
}
