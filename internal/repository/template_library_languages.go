package repository

import (
	"embed"
	"maps"
	"slices"
	"strings"
)

//go:embed template_library/*.py template_library/*.ps1 template_library/terraform_*.sh
var libraryLanguageScripts embed.FS

func libraryLanguageVariants(originals []LibraryTemplate) []LibraryTemplate {
	var variants []LibraryTemplate
	for _, language := range []struct {
		slug        string
		name        string
		interpreter string
		extension   string
	}{
		{slug: "powershell", name: "PowerShell 7", interpreter: "pwsh", extension: ".ps1"},
		{slug: "python", name: "Python 3", interpreter: "python3", extension: ".py"},
	} {
		for _, original := range originals {
			entry := original
			entry.Slug += "-" + language.slug
			entry.Snapshot.Name += " (" + language.name + ")"
			entry.Configuration = maps.Clone(original.Configuration)
			entry.Permissions = slices.Clone(original.Permissions)
			entry.Snapshot.Variables = slices.Clone(original.Snapshot.Variables)
			entry.Snapshot.Steps = slices.Clone(original.Snapshot.Steps)
			entry.Dependencies = []string{language.name, "Linux host agent"}
			for _, dependency := range original.Dependencies {
				if dependency != "Bash" && dependency != "curl" &&
					dependency != "GNU mv" {
					entry.Dependencies = append(entry.Dependencies, dependency)
				}
			}
			for index := range entry.Snapshot.Steps {
				step := &entry.Snapshot.Steps[index]
				step.Interpreter = language.interpreter
				step.ScriptBody = libraryScript(
					libraryScriptFile(step.Name) + language.extension,
				)
				step.VariableNames = slices.Clone(step.VariableNames)
			}
			variants = append(variants, entry)
		}
	}
	return variants
}

func libraryScriptFile(stepName string) string {
	switch stepName {
	case "Download artifact":
		return "download"
	case "Install file":
		return "copy"
	case "Restart service":
		return "restart"
	case "HTTP health check":
		return "health"
	case "Pull Compose images":
		return "compose_pull"
	case "Start Compose application":
		return "compose_up"
	default:
		panic("missing bundled script for " + stepName)
	}
}

func libraryScript(name string) string {
	script, err := libraryLanguageScripts.ReadFile("template_library/" + name)
	if err != nil {
		// A missing embedded asset is a programmer error, not user input.
		panic(err)
	}
	return string(script)
}

func terraformLibraryWorkflow() LibraryTemplate {
	plan := libraryStep(
		"Terraform init, validate, and plan",
		libraryScript("terraform_plan.sh"),
		"TF_CONFIG_DIR",
		"TF_HTTP_USERNAME",
		"TF_HTTP_PASSWORD",
	)
	plan.TimeoutSeconds = 900
	plan.ExecutionTarget = "local"
	plan.ContainerImage = "operator/terraform-toolchain:configure"
	plan.NetworkMode = "bridge"
	plan.ApprovalArtifactPath = "terraform-context/tfplan"
	plan.ApprovalReviewPath = "terraform-context/review.json"
	plan.ApprovalReviewFormat = "terraform"
	apply := libraryStep(
		"Apply approved Terraform plan",
		libraryScript("terraform_apply.sh"),
		"TF_HTTP_USERNAME",
		"TF_HTTP_PASSWORD",
	)
	apply.SortOrder = 2
	apply.TimeoutSeconds = 900
	apply.ExecutionTarget = "local"
	apply.ContainerImage = plan.ContainerImage
	apply.NetworkMode = "bridge"
	return LibraryTemplate{
		Slug:    "terraform-plan-apply",
		Version: "1",
		Kind:    "workflow",
		Dependencies: []string{
			"Server container runtime",
			"Operator-provided Linux image containing Bash, Python 3, Terraform, and required provider plugins",
			"GNU cp for copying context without private SELinux labels",
			"Configuration baked into the image with .terraform.lock.hcl",
			"Remote state backend with locking and network access from the container",
			"The same reviewed toolchain image for both steps",
		},
		Permissions: []string{
			"Read access to the toolchain image, configuration, and provider plugins",
			"Access to remote state, its lock, and the intended infrastructure",
			"Deployment artifact approval permission before the apply step",
			"Treat configuration, binary plans, and state as sensitive data",
		},
		Configuration: map[string]LibraryVariableConfig{
			"TF_CONTAINER_IMAGE": {
				Description: "Reviewed toolchain image containing Bash, Python 3, Terraform, providers, and configuration. Both steps use this image.",
				Required:    true,
				Format:      "image",
			},
			"TF_CONFIG_DIR": {
				Description: "Absolute configuration directory inside the toolchain image. Include a provider lock file and a remote backend with locking.",
				Required:    true,
				Format:      "path",
			},
			"TF_HTTP_USERNAME": {
				Description: "Optional HTTP backend username. Configure other backend credentials through editable secret variables and step restrictions.",
				Format:      "text",
			},
			"TF_HTTP_PASSWORD": {
				Description: "Optional fresh HTTP backend password or token. Stored as an encrypted secret variable.",
				Format:      "text",
			},
		},
		Snapshot: ProjectTemplateSnapshot{
			Name:        "Terraform plan, approve, and apply",
			Description: "Initialize and validate existing configuration, review a saved plan, then apply only the approved plan. Requires a remote backend.",
			Steps:       []ProjectTemplateStep{plan, apply},
			Variables: []ProjectTemplateVariable{
				{Key: "TF_CONTAINER_IMAGE", Name: "TF_CONTAINER_IMAGE"},
				{Key: "TF_CONFIG_DIR", Name: "TF_CONFIG_DIR"},
				{Key: "TF_HTTP_USERNAME", Name: "TF_HTTP_USERNAME"},
				{
					Key:    "TF_HTTP_PASSWORD",
					Name:   "TF_HTTP_PASSWORD",
					Secret: true,
				},
			},
		},
	}
}

// LibraryUsesAgents identifies entries that require registered host selectors.
func LibraryUsesAgents(entry LibraryTemplate) bool {
	for _, step := range entry.Snapshot.Steps {
		if step.ExecutionTarget == "agent" {
			return true
		}
	}
	return false
}

// LibraryInterpreters lists the actual interpreters used by the entry's steps.
func LibraryInterpreters(entry LibraryTemplate) string {
	var names []string
	for _, step := range entry.Snapshot.Steps {
		if !slices.Contains(names, step.Interpreter) {
			names = append(names, step.Interpreter)
		}
	}
	return strings.Join(names, ", ")
}
