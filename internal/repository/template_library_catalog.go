package repository

import (
	"database/sql"
	_ "embed" // Embed the bundled starter scripts with go:embed.
)

//go:embed template_library/download.sh
var libraryDownload string

//go:embed template_library/copy.sh
var libraryCopy string

//go:embed template_library/restart.sh
var libraryRestart string

//go:embed template_library/health.sh
var libraryHealth string

//go:embed template_library/compose_pull.sh
var libraryComposePull string

//go:embed template_library/compose_up.sh
var libraryComposeUp string

// LibraryTemplate is bundled with the binary. Applying it creates independent
// project records, rather than references to a mutable library definition.
// swagger:model LibraryTemplate
type LibraryTemplate struct {
	Slug          string                           `json:"slug"`
	Version       string                           `json:"version"`
	Kind          string                           `json:"kind"`
	Dependencies  []string                         `json:"dependencies"`
	Permissions   []string                         `json:"permissions"`
	Configuration map[string]LibraryVariableConfig `json:"configuration"`
	Snapshot      ProjectTemplateSnapshot          `json:"snapshot"`
}

// swagger:model LibraryVariableConfig
type LibraryVariableConfig struct {
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Format      string `json:"format"`
}

func libraryStep(
	name, script string,
	variableNames ...string,
) ProjectTemplateStep {
	return ProjectTemplateStep{
		Name:               name,
		ScriptBody:         script,
		Interpreter:        "bash",
		SortOrder:          1,
		ExecutionTarget:    "agent",
		AgentExecutionMode: "host",
		AgentSelectors:     []string{},
		VariableNames:      variableNames,
		TimeoutSeconds:     300,
	}
}

// TemplateLibrary returns fresh definitions so callers cannot edit the catalog.
// ponytail: bundled entries; add publishing/version storage only if needed.
func TemplateLibrary() []LibraryTemplate {
	downloadArtifact := libraryStep(
		"Download artifact",
		libraryDownload,
		"ARTIFACT_URL",
		"ARTIFACT_TOKEN",
	)
	installFile := libraryStep(
		"Install file",
		libraryCopy,
		"FILE_SOURCE",
		"INSTALL_PATH",
	)
	restartService := libraryStep(
		"Restart service",
		libraryRestart,
		"SERVICE_NAME",
	)
	checkHTTPHealth := libraryStep(
		"HTTP health check",
		libraryHealth,
		"HEALTH_URL",
	)
	pullComposeImages := libraryStep(
		"Pull Compose images",
		libraryComposePull,
		"COMPOSE_FILE",
		"COMPOSE_PROJECT",
		"APP_IMAGE",
	)
	startComposeApplication := libraryStep(
		"Start Compose application",
		libraryComposeUp,
		"COMPOSE_FILE",
		"COMPOSE_PROJECT",
		"APP_IMAGE",
	)

	definitions := []struct {
		slug         string
		kind         string
		name         string
		purpose      string
		steps        []ProjectTemplateStep
		dependencies []string
		permissions  []string
	}{
		{
			slug:    "artifact-download",
			kind:    "step",
			name:    "Download an artifact",
			purpose: "Download one file over HTTPS into managed deployment staging.",
			steps:   []ProjectTemplateStep{downloadArtifact},
			dependencies: []string{
				"Bash",
				"curl",
				"GNU mv",
				"HTTPS access and trusted CA certificates",
			},
			permissions: []string{
				"Write access to managed staging",
			},
		},
		{
			slug:    "file-copy",
			kind:    "step",
			name:    "Copy or install a file",
			purpose: "Install one file with mode 0755. Deployment execution replaces the destination file.",
			steps:   []ProjectTemplateStep{installFile},
			dependencies: []string{
				"Bash",
				"GNU install",
				"Existing destination directory",
			},
			permissions: []string{
				"Read access to source file",
				"Write access to destination directory",
			},
		},
		{
			slug:    "service-restart",
			kind:    "step",
			name:    "Restart a Linux service",
			purpose: "Restart an existing systemd service and verify its active state.",
			steps:   []ProjectTemplateStep{restartService},
			dependencies: []string{
				"Bash",
				"systemd",
				"An existing service unit",
			},
			permissions: []string{
				"Agent account authorized to restart the selected service without an interactive prompt",
			},
		},
		{
			slug:    "http-health-check",
			kind:    "step",
			name:    "Check HTTP health",
			purpose: "Require a successful HTTP response with bounded connection and request timeouts.",
			steps:   []ProjectTemplateStep{checkHTTPHealth},
			dependencies: []string{
				"Bash",
				"curl",
				"Network access to the health endpoint",
			},
			permissions: []string{
				"Read access to the health endpoint",
			},
		},
		{
			slug:    "linux-service",
			kind:    "workflow",
			name:    "Deploy a Linux service",
			purpose: "Download and install a single service executable, restart its existing unit, then check HTTP health.",
			steps: []ProjectTemplateStep{
				downloadArtifact,
				installFile,
				restartService,
				checkHTTPHealth,
			},
			dependencies: []string{
				"Bash",
				"curl",
				"GNU mv",
				"GNU install",
				"systemd",
				"Provisioned service and destination directory",
				"HTTPS artifact source",
			},
			permissions: []string{
				"Write access to destination directory",
				"Permission to restart the selected service",
				"Network access to artifact and health endpoints",
			},
		},
		{
			slug:    "compose-application",
			kind:    "workflow",
			name:    "Deploy a Compose application",
			purpose: "Pull images, start an existing Docker Compose application, then check HTTP health.",
			steps: []ProjectTemplateStep{
				pullComposeImages,
				startComposeApplication,
				checkHTTPHealth,
			},
			dependencies: []string{
				"Bash",
				"curl",
				"Docker with Compose v2",
				"Existing Compose file using ${APP_IMAGE}",
				"Registry login configured on the agent when required",
			},
			permissions: []string{
				"Agent account authorized to use Docker",
				"Read access to the Compose file",
				"Network access to registry and health endpoint",
			},
		},
	}

	variables := map[string]struct {
		description string
		format      string
		required    bool
		secret      bool
	}{
		"ARTIFACT_URL": {
			description: "HTTPS URL for the single artifact file. Supply authentication through ARTIFACT_TOKEN.",
			format:      "https-url",
			required:    true,
			secret:      false,
		},
		"ARTIFACT_TOKEN": {
			description: "Optional fresh bearer token. Stored as a secret variable. Leave empty for a public artifact.",
			format:      "bearer",
			required:    false,
			secret:      true,
		},
		"FILE_SOURCE": {
			description: "Absolute source path. Leave empty to use artifact in DURPDEPLOY_STAGE_DIR.",
			format:      "optional-path",
			required:    false,
			secret:      false,
		},
		"INSTALL_PATH": {
			description: "Absolute destination file path. Its directory must already exist.",
			format:      "path",
			required:    true,
			secret:      false,
		},
		"SERVICE_NAME": {
			description: "Existing systemd service unit, for example my-app.service.",
			format:      "service",
			required:    true,
			secret:      false,
		},
		"HEALTH_URL": {
			description: "HTTP or HTTPS endpoint that must return a successful response.",
			format:      "http-url",
			required:    true,
			secret:      false,
		},
		"COMPOSE_FILE": {
			description: "Absolute path to a pre-provisioned Compose file. Use ${APP_IMAGE} for its application image.",
			format:      "path",
			required:    true,
			secret:      false,
		},
		"COMPOSE_PROJECT": {
			description: "Compose project name. The selected project identifies the application to update.",
			format:      "compose-project",
			required:    true,
			secret:      false,
		},
		"APP_IMAGE": {
			description: "Application image reference consumed by the Compose file.",
			format:      "image",
			required:    true,
			secret:      false,
		},
	}

	templates := make([]LibraryTemplate, 0, len(definitions))
	for _, definition := range definitions {
		template := LibraryTemplate{
			Slug:          definition.slug,
			Version:       "1",
			Kind:          definition.kind,
			Dependencies:  definition.dependencies,
			Permissions:   definition.permissions,
			Configuration: map[string]LibraryVariableConfig{},
			Snapshot: ProjectTemplateSnapshot{
				Name:        definition.name,
				Description: definition.purpose,
				Steps:       definition.steps,
				Variables:   []ProjectTemplateVariable{},
			},
		}
		for stepIndex := range template.Snapshot.Steps {
			step := &template.Snapshot.Steps[stepIndex]
			step.SortOrder = int64(stepIndex + 1)
			for _, variableName := range step.VariableNames {
				if _, exists := template.Configuration[variableName]; exists {
					continue
				}
				variable := variables[variableName]
				template.Configuration[variableName] = LibraryVariableConfig{
					Description: variable.description,
					Required:    variable.required,
					Format:      variable.format,
				}
				template.Snapshot.Variables = append(
					template.Snapshot.Variables,
					ProjectTemplateVariable{
						Key:    variableName,
						Name:   variableName,
						Secret: variable.secret,
					},
				)
			}
		}
		templates = append(templates, template)
	}
	variants := libraryLanguageVariants(templates)
	templates = append(templates, variants...)
	return append(templates, terraformLibraryWorkflow())
}

func GetLibraryTemplate(slug string) (LibraryTemplate, error) {
	for _, entry := range TemplateLibrary() {
		if entry.Slug == slug {
			return entry, nil
		}
	}
	return LibraryTemplate{}, sql.ErrNoRows
}
