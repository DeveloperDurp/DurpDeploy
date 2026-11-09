package repository

import (
	"database/sql"
	"errors"
)

var ErrAnsibleCheckMode = errors.New(
	"ansible check mode requires an Ansible step",
)

// AnsibleCheckMode captures an explicit choice, distinct from legacy NULL runs.
func AnsibleCheckMode(enabled bool) sql.NullInt64 {
	mode := sql.NullInt64{Valid: true}
	if enabled {
		mode.Int64 = 1
	}
	return mode
}

func validateAnsibleCheckMode(
	mode int64,
	steps []DeploymentStepSnapshot,
) error {
	if mode != 1 {
		return nil
	}
	for _, step := range steps {
		if step.Kind == "ansible" {
			return nil
		}
	}
	return ErrAnsibleCheckMode
}
