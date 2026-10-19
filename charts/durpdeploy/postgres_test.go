package chart_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"
)

type secretReference struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type environment struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	ValueFrom struct {
		SecretKeyRef secretReference `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}

type chartResource struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Ports []struct {
			Name       string `yaml:"name"`
			Port       int    `yaml:"port"`
			TargetPort string `yaml:"targetPort"`
		} `yaml:"ports"`
		Template struct {
			Spec struct {
				Containers []struct {
					Env   []environment `yaml:"env"`
					Ports []struct {
						Name string `yaml:"name"`
						Port int    `yaml:"containerPort"`
					} `yaml:"ports"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
	StringData map[string]string `yaml:"stringData"`
}

func renderChart(t *testing.T, values string) []chartResource {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("Helm is required for chart connection tests")
	}
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("helm", "template", "connection", ".",
		"--namespace", "default", "-f", path)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("render chart: %v", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	var resources []chartResource
	for {
		var resource chartResource
		err := decoder.Decode(&resource)
		if err == io.EOF {
			return resources
		}
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, resource)
	}
}

func TestPostgresConnectionFields(t *testing.T) {
	// Given: synthetic credentials containing URI and Kubernetes metacharacters.
	const password = "synthetic:@/?#%&=+ $()'\\"
	const username = "chart user:@/?#%&=+$(OTHER)"
	const database = "chart database:@/?#%&=+$(OTHER)"
	for _, fixture := range []struct {
		name   string
		values string
		secret string
	}{
		{"default", "", "connection-durpdeploy-postgres"},
		{"existing", "postgres:\n  existingSecret: operator-postgres\n",
			"operator-postgres"},
		{"literal", "postgres:\n  enabled: true\n  password: " +
			"\"synthetic:@/?#%&=+ $()'\\\\\"\n",
			"connection-durpdeploy-postgres"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Setenv("PGPASSWORD", password)
			t.Setenv("PGSERVICE", "")
			t.Setenv("PGUSER", "ignored-user")
			t.Setenv("PGDATABASE", "ignored-database")
			t.Setenv("PGHOST", "ignored-host")
			t.Setenv("PGPORT", "1234")
			values := fixture.values + "externalPostgres:\n" +
				"  host: '::1'\n  port: 5433\n  sslmode: disable\n" +
				"  username: '" + username + "'\n" +
				"  database: '" + database + "'\n"
			// When: Helm renders the connection consumed by the installed driver.
			resources := renderChart(t, values)
			var dsn string
			var reference secretReference
			passwordRendered := false
			for _, resource := range resources {
				if resource.Kind == "Secret" &&
					resource.StringData["password"] != "" {
					passwordRendered = true
					if resource.StringData["password"] != password {
						t.Fatal("rendered password changed")
					}
				}
				if resource.Kind != "Deployment" {
					continue
				}
				passwordIndex, dsnIndex := -1, -1
				for i, env := range resource.Spec.Template.Spec.Containers[0].Env {
					switch env.Name {
					case "PGPASSWORD":
						reference = env.ValueFrom.SecretKeyRef
						passwordIndex = i
					case "DURPDEPLOY_DB":
						dsn = env.Value
						dsnIndex = i
					}
				}
				if passwordIndex < 0 || passwordIndex >= dsnIndex {
					t.Fatal(
						"password must be available before database configuration",
					)
				}
			}
			// Then: the configured fields and raw password survive without interpolation.
			if passwordRendered != (fixture.name == "literal") {
				t.Fatal("password Secret rendering did not match its source")
			}
			if reference != (secretReference{fixture.secret, "password"}) {
				t.Fatal("wrong password Secret reference")
			}
			if strings.Contains(dsn, "$(") || strings.Contains(dsn, password) {
				t.Fatal(
					"DSN contains environment interpolation or a raw password",
				)
			}
			config, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if config.User != username || config.Database != database ||
				config.Host != "::1" || config.Port != 5433 ||
				config.Password != password || config.TLSConfig != nil {
				t.Fatal(
					"driver did not preserve the configured connection fields",
				)
			}
		})
	}
}

func TestPostgresFullDSNSecret(t *testing.T) {
	// Given: an operator-managed DSN with its own credentials and options.
	values := "externalPostgres:\n  dsnSecret:\n" +
		"    name: complete-connection\n    key: connection-uri\n"
	// When: Helm renders the Deployment.
	resources := renderChart(t, values)
	// Then: only the complete DSN Secret supplies database credentials.
	found := false
	for _, resource := range resources {
		if resource.Kind != "Deployment" {
			continue
		}
		for _, env := range resource.Spec.Template.Spec.Containers[0].Env {
			switch env.Name {
			case "PGPASSWORD", "DURPDEPLOY_PG_PASSWORD":
				t.Fatal("full DSN must not depend on a password Secret")
			case "DURPDEPLOY_DB":
				found = true
				if env.Value != "" || env.ValueFrom.SecretKeyRef !=
					(secretReference{"complete-connection", "connection-uri"}) {
					t.Fatal("full DSN Secret override was not preserved")
				}
			}
		}
	}
	if !found {
		t.Fatal("database configuration missing")
	}
}
