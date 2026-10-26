package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const FileEnv = "DURPDEPLOY_CONFIG_FILE"

type Lookup func(string) (string, bool)

// Load resolves the server configuration without modifying process state.
func Load() (Config, error) { return LoadWithLookup(os.LookupEnv) }

// LoadWithLookup supports isolated callers and tests. Environment values take
// precedence over YAML, including explicitly empty strings. An empty selector
// disables file loading. YAML paths resolve from the process working directory.
func LoadWithLookup(lookup Lookup) (Config, error) {
	var reader io.Reader
	if path, _ := lookup(FileEnv); path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("cannot read %s", FileEnv)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	return resolve(reader, lookup)
}

func resolve(reader io.Reader, lookup Lookup) (Config, error) {
	cfg := Defaults()
	cfg.supplied = make(map[string]string)
	value := reflect.ValueOf(&cfg).Elem()
	if reader != nil {
		decoder := yaml.NewDecoder(reader)
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			return Config{}, errors.New("invalid YAML configuration")
		}
		if len(document.Content) != 1 {
			return Config{}, errors.New("expected one YAML mapping")
		}
		if err := applyYAML(document.Content[0], value, "", cfg.supplied); err != nil {
			return Config{}, err
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return Config{}, errors.New("expected one YAML document")
		}
	}
	if err := visitFields(value, "", func(v reflect.Value, path, env string) error {
		raw, set := lookup(env)
		if !set {
			return nil
		}
		if err := setEnvironment(v, raw); err != nil {
			return fmt.Errorf("invalid value for %s", env)
		}
		cfg.supplied[env] = raw
		return nil
	}); err != nil {
		return Config{}, err
	}
	if err := cfg.Limits.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Parsing errors intentionally omit raw YAML and environment values. They can
// contain database passwords, SMTP passwords, or the application secret key.
func applyYAML(
	node *yaml.Node, value reflect.Value, path string,
	supplied map[string]string,
) error {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return errors.New("configuration sections must be YAML mappings")
	}
	seen := make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		key, child := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" ||
			seen[key.Value] {
			return errors.New("configuration keys must be unique strings")
		}
		seen[key.Value] = true
		index := fieldIndex(value.Type(), key.Value)
		if index < 0 {
			return errors.New("unknown YAML configuration key")
		}
		field := value.Type().Field(index)
		name := joinPath(path, field.Tag.Get("yaml"))
		v := value.Field(index)
		if v.Kind() == reflect.Struct {
			if err := applyYAML(child, v, name, supplied); err != nil {
				return err
			}
			continue
		}
		if err := applyScalar(child, v, name); err != nil {
			return err
		}
		supplied[envName(field, name)] = scalarString(v)
	}
	return nil
}

func applyScalar(node *yaml.Node, value reflect.Value, name string) error {
	if node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return fmt.Errorf("%s must be a non-null scalar", name)
	}
	if value.Kind() == reflect.String {
		// Existing environment-backed settings retain their textual
		// validators. YAML booleans and numbers use the same spelling.
		if node.Tag != "!!str" && node.Tag != "!!bool" && node.Tag != "!!int" {
			return fmt.Errorf("invalid scalar type for %s", name)
		}
		value.SetString(node.Value)
		return nil
	}
	integer := node.Tag == "!!int"
	floating := value.Kind() == reflect.Float64 && node.Tag == "!!float"
	if !integer && !floating {
		return fmt.Errorf("invalid scalar type for %s", name)
	}
	if err := node.Decode(value.Addr().Interface()); err != nil {
		return fmt.Errorf("invalid scalar type for %s", name)
	}
	return nil
}

func fieldIndex(t reflect.Type, name string) int {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.IsExported() && field.Tag.Get("yaml") == name {
			return i
		}
	}
	return -1
}

func visitFields(
	value reflect.Value, path string,
	visit func(reflect.Value, string, string) error,
) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		name := joinPath(path, field.Tag.Get("yaml"))
		v := value.Field(i)
		if v.Kind() == reflect.Struct {
			if err := visitFields(v, name, visit); err != nil {
				return err
			}
		} else if err := visit(v, name, envName(field, name)); err != nil {
			return err
		}
	}
	return nil
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func envName(field reflect.StructField, path string) string {
	if name := field.Tag.Get("env"); name != "" {
		return name
	}
	return "DURPDEPLOY_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

func setEnvironment(value reflect.Value, raw string) error {
	switch value.Kind() {
	case reflect.String:
		value.SetString(raw)
	case reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		value.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		value.SetFloat(n)
	default:
		return errors.New("unsupported configuration type")
	}
	return nil
}

func scalarString(value reflect.Value) string {
	switch value.Kind() {
	case reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64)
	default:
		return value.String()
	}
}
