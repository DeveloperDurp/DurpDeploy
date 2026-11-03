package repository

import (
	"context"
	"errors"
	"slices"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/stepdefinition"
)

// ValidateAnsibleCredential reads metadata only. Environment zero is an editor
// check; admission must supply the accepted environment.
func (r *Repository) ValidateAnsibleCredential(
	ctx context.Context,
	projectID int64,
	name string,
	environmentID int64,
) error {
	project, err := r.Queries.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	local, err := r.Queries.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return err
	}
	inherited, err := r.inheritedVariables(ctx, project, local, true)
	if err != nil {
		return err
	}
	rows := append([]db.Variable(nil), local...)
	for _, row := range inherited.Variables {
		if row.Override == nil {
			rows = append(
				rows,
				db.Variable{
					Name:          row.Name,
					EnvironmentID: row.EnvironmentID,
					Secret:        row.Secret,
					IsCredential:  row.IsCredential,
				},
			)
		}
	}
	if !ansibleCredentialAvailable(rows, name, environmentID) {
		return errors.Join(
			stepdefinition.ErrInvalidAnsible,
			errors.New(
				"SSH key reference must resolve to an accessible secret credential",
			),
		)
	}

	return nil
}

func ansibleCredentialAvailable(
	rows []db.Variable,
	name string,
	environmentID int64,
) bool {
	var global, scoped *db.Variable
	for i := range rows {
		row := &rows[i]
		if row.Name != name {
			continue
		}
		if !row.EnvironmentID.Valid {
			global = row
		} else if row.EnvironmentID.Int64 == environmentID {
			scoped = row
		}
		if environmentID == 0 && row.Secret != 0 && row.IsCredential != 0 {
			return true
		}
	}
	if scoped != nil {
		global = scoped
	}
	return global != nil && global.Secret != 0 && global.IsCredential != 0
}

// ValidateAnsibleReferences validates editor references without decrypting values.
func (r *Repository) ValidateAnsibleReferences(
	ctx context.Context,
	projectID int64,
	config *stepdefinition.Ansible,
) error {
	if config.Connection != "local" {
		if err := r.ValidateAnsibleCredential(ctx, projectID, config.SSHKeyVariable, 0); err != nil {
			return err
		}
	}
	project, err := r.Queries.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	catalog, err := r.AvailableVariables(
		ctx,
		project,
		VariableContext{
			Kind:        "ansible",
			Interpreter: "bash",
			Target:      "local",
			AgentMode:   "host",
		},
	)
	if err != nil {
		return err
	}
	available := map[string]bool{}
	for _, variable := range catalog.Configured {
		available[variable.Name] = true
	}
	for _, variable := range catalog.Predefined {
		available[variable.Name] = true
	}
	for _, source := range config.ExtraVars {
		if !available[source] {
			return errors.Join(
				stepdefinition.ErrInvalidAnsible,
				errors.New("mapped variable is unavailable in this project"),
			)
		}
	}
	return nil
}

func (r *Repository) validateAnsibleCapturedVariables(
	ctx context.Context,
	deployment db.Deployment,
	steps []DeploymentStepSnapshot,
) error {
	if !slices.ContainsFunc(
		steps,
		func(step DeploymentStepSnapshot) bool { return step.Kind == stepdefinition.AnsibleKind },
	) {
		return nil
	}
	variables, err := r.ListDeploymentVariables(ctx, deployment.ID)
	if err != nil {
		return err
	}
	global, scoped := map[string]db.ReleaseVariable{}, map[string]db.ReleaseVariable{}
	for _, variable := range variables {
		if !variable.EnvironmentID.Valid {
			global[variable.Name] = variable
		} else if variable.EnvironmentID.Int64 == deployment.EnvironmentID {
			scoped[variable.Name] = variable
		}
	}
	for name, variable := range scoped {
		global[name] = variable
	}
	for _, step := range steps {
		if step.Kind != stepdefinition.AnsibleKind {
			continue
		}
		if err := validateAnsibleCapturedStep(step, global); err != nil {
			return err
		}
	}
	return nil
}

func validateAnsibleCapturedStep(
	step DeploymentStepSnapshot,
	global map[string]db.ReleaseVariable,
) error {
	a, err := stepdefinition.DecodeAnsible(step.AnsibleJson)
	if err != nil || a == nil {
		return stepdefinition.ErrInvalidAnsible
	}
	if key, exists := global[a.SSHKeyVariable]; a.Connection != "local" &&
		(!exists || key.Secret == 0) {
		return errors.Join(
			stepdefinition.ErrInvalidAnsible,
			errors.New(
				"SSH credential is unavailable in the accepted variable snapshot",
			),
		)
	}
	for _, source := range a.ExtraVars {
		if containerenv.IsEnvironmentIdentity(source) ||
			source == "ARTIFACT_PATH" ||
			source == containerenv.StageVariable ||
			source == containerenv.ApprovedVariable {
			continue
		}
		if _, exists := global[source]; !exists {
			return errors.Join(
				stepdefinition.ErrInvalidAnsible,
				errors.New(
					"mapped variable is unavailable in the accepted environment",
				),
			)
		}
	}
	return nil
}

func (r *Repository) validateAnsibleAdmission(
	ctx context.Context,
	projectID, environmentID int64,
	steps []DeploymentStepSnapshot,
) error {
	for _, step := range steps {
		if step.Kind != stepdefinition.AnsibleKind {
			continue
		}
		config, err := stepdefinition.DecodeAnsible(step.AnsibleJson)
		if err != nil {
			return err
		}
		if config == nil {
			return stepdefinition.ErrInvalidAnsible
		}
		if config.Connection != "local" {
			if err := r.ValidateAnsibleCredential(ctx, projectID, config.SSHKeyVariable, environmentID); err != nil {
				return err
			}
		}
	}
	return nil
}
