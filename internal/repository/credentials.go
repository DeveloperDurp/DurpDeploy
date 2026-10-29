package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"durpdeploy/internal/db"
)

var (
	ErrCredentialInput    = errors.New("invalid credential rotation fields")
	ErrCredentialConflict = errors.New(
		"credential changed; reload before saving",
	)
	ErrCredentialAmbiguous = errors.New(
		"multiple variables share this name and environment; remove duplicates before rotation",
	)
)

// Credential contains public metadata only. Values never leave the repository.
// swagger:model Credential
type Credential struct {
	Inherited     bool   `json:"inherited"`
	LifecycleID   int64  `json:"lifecycle_id,omitempty"`
	Kind          string `json:"kind"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	EnvironmentID *int64 `json:"environment_id"`
	ExpiresAt     int64  `json:"expires_at"`
	RotationDueAt int64  `json:"rotation_due_at"`
	RotatedAt     int64  `json:"rotated_at"`
	Revision      int64  `json:"revision"`
	Status        string `json:"status"`
	Active        bool   `json:"active"`
}

// swagger:model CredentialRotationRequest
// Send both dates as Unix seconds. Zero clears a date.
// Include the revision from the latest credential read.
// PUT preserves the secret when value is empty and rotates it otherwise.
type CredentialRotationInput struct {
	Value         string `json:"value"`
	ExpiresAt     int64  `json:"expires_at"`
	RotationDueAt int64  `json:"rotation_due_at"`
	Revision      int64  `json:"revision"`
}

// swagger:model CredentialRotationResult
type CredentialRotationResult struct {
	Credential              Credential `json:"credential"`
	ReleaseVariablesUpdated int64      `json:"release_variables_updated"`
}

func (c *Credential) setStatus(now time.Time) {
	due := c.RotationDueAt
	if c.ExpiresAt > 0 && (due == 0 || c.ExpiresAt < due) {
		due = c.ExpiresAt
	}
	switch {
	case c.ExpiresAt > 0 && c.ExpiresAt <= now.Unix():
		c.Status = "expired"
	case due > 0 && due <= now.Unix():
		c.Status = "due"
	case due > 0 && due <= now.Add(30*24*time.Hour).Unix():
		c.Status = "due_soon"
	case due > 0:
		c.Status = "current"
	default:
		c.Status = "unknown"
	}
}

func credentialVariable(v db.Variable) Credential {
	c := Credential{Kind: "variable", ID: v.ID, Name: v.Name,
		ExpiresAt: v.ExpiresAt, RotationDueAt: v.RotationDueAt,
		RotatedAt: v.RotatedAt, Revision: v.RotationRevision, Active: true}
	if v.EnvironmentID.Valid {
		id := v.EnvironmentID.Int64
		c.EnvironmentID = &id
	}
	c.setStatus(time.Now())
	return c
}

func credentialLifecycle(v db.LifecycleVariable) Credential {
	c := credentialVariable(db.Variable{ID: v.ID, Name: v.Name,
		EnvironmentID: v.EnvironmentID, ExpiresAt: v.ExpiresAt,
		RotationDueAt: v.RotationDueAt, RotatedAt: v.RotatedAt,
		RotationRevision: v.RotationRevision})
	c.Kind = "lifecycle"
	return c
}

func credentialPackage(v db.PackageRepository, active bool) Credential {
	c := Credential{Kind: "package", ID: v.ID, Name: v.UrlTemplate,
		ExpiresAt: v.ExpiresAt, RotationDueAt: v.RotationDueAt,
		RotatedAt: v.RotatedAt, Revision: v.RotationRevision, Active: active}
	c.setStatus(time.Now())
	return c
}

// ListCredentials reads ciphertext rows without decrypting any secret.
func (r *Repository) ListCredentials(ctx context.Context, ownerID int64,
	lifecycle bool,
) ([]Credential, error) {
	if lifecycle {
		return r.listLifecycleCredentials(ctx, ownerID)
	}
	project, err := r.Queries.GetProject(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	variables, err := r.Queries.ListVariablesByProject(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	inherited, err := r.inheritedVariables(ctx, project, variables, true)
	if err != nil {
		return nil, err
	}
	return r.ProjectCredentials(ctx, ownerID, variables, inherited)
}

// ProjectCredentials reuses the overview's loaded variables and inheritance.
// It reads only package metadata and never uses variable values.
func (r *Repository) ProjectCredentials(
	ctx context.Context,
	ownerID int64,
	variables []db.Variable,
	inherited VariableInheritance,
) ([]Credential, error) {
	result := []Credential{}
	for _, row := range variables {
		if row.IsCredential != 0 {
			result = append(result, credentialVariable(row))
		}
	}
	result = append(result, inheritedCredentials(inherited)...)
	selected, err := r.Queries.GetProjectArtifactRepository(ctx, ownerID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	sources, err := r.Queries.ListPackageRepositories(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for _, row := range sources {
		if row.AuthType != "noauth" {
			result = append(
				result,
				credentialPackage(row, row.ID == selected.ID),
			)
		}
	}
	return result, nil
}

func (r *Repository) listLifecycleCredentials(
	ctx context.Context,
	ownerID int64,
) ([]Credential, error) {
	result := []Credential{}
	if _, err := r.Queries.GetLifecycle(ctx, ownerID); err != nil {
		return nil, err
	}
	rows, err := r.Queries.ListLifecycleVariables(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.IsCredential != 0 {
			result = append(result, credentialLifecycle(row))
		}
	}
	return result, nil
}

func inheritedCredentials(inherited VariableInheritance) []Credential {
	result := []Credential{}
	for _, row := range inherited.Variables {
		if row.IsCredential != 0 && row.Override == nil {
			credential := credentialLifecycle(row.LifecycleVariable)
			credential.Inherited = true
			credential.LifecycleID = inherited.Lifecycle.ID
			result = append(result, credential)
		}
	}
	return result
}

// CredentialStatusSummary is the project overview banner contract.
// swagger:model CredentialStatusSummary
type CredentialStatusSummary struct {
	Expired int `json:"expired"`
	Due     int `json:"due"`
	DueSoon int `json:"due_soon"`
}

func SummarizeCredentials(credentials []Credential) CredentialStatusSummary {
	var result CredentialStatusSummary
	for _, credential := range credentials {
		switch credential.Status {
		case "expired":
			result.Expired++
		case "due":
			result.Due++
		case "due_soon":
			result.DueSoon++
		}
	}
	return result
}
