package repository

import (
	"context"
	"database/sql"
	"strings"

	"durpdeploy/internal/db"
)

func (r *Repository) CreateStepTemplateWithPlacement(
	ctx context.Context,
	params db.CreateStepTemplateParams,
	target string,
	selectors []string,
) (db.StepTemplate, error) {
	var template db.StepTemplate
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		template, err = q.CreateStepTemplate(ctx, params)
		if err != nil {
			return err
		}
		if err := setTemplatePlacement(
			ctx,
			q,
			template.ID,
			target,
			selectors,
		); err != nil {
			return err
		}
		version, err := q.CreateStepTemplateVersion(
			ctx,
			db.CreateStepTemplateVersionParams{
				TemplateID:    template.ID,
				VersionNumber: 1,
				Name:          template.Name,
				ScriptBody:    template.ScriptBody,
				Interpreter:   template.Interpreter,
			},
		)
		if err != nil {
			return err
		}
		return setTemplateVersionPlacement(
			ctx,
			q,
			version.ID,
			target,
			selectors,
		)
	})
	template.ExecutionTarget = target
	return template, err
}

func (r *Repository) UpdateStepTemplateWithPlacement(
	ctx context.Context,
	params db.UpdateStepTemplateParams,
	target string,
	selectors []string,
) (db.StepTemplate, error) {
	var template db.StepTemplate
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		template, err = q.UpdateStepTemplate(ctx, params)
		if err != nil {
			return err
		}
		if err := setTemplatePlacement(
			ctx,
			q,
			template.ID,
			target,
			selectors,
		); err != nil {
			return err
		}
		latest, err := q.GetLatestStepTemplateVersionNumber(
			ctx,
			template.ID,
		)
		if err != nil {
			return err
		}
		version, err := q.CreateStepTemplateVersion(
			ctx,
			db.CreateStepTemplateVersionParams{
				TemplateID:    template.ID,
				VersionNumber: latest + 1,
				Name:          template.Name,
				ScriptBody:    template.ScriptBody,
				Interpreter:   template.Interpreter,
			},
		)
		if err != nil {
			return err
		}
		return setTemplateVersionPlacement(
			ctx,
			q,
			version.ID,
			target,
			selectors,
		)
	})
	template.ExecutionTarget = target
	return template, err
}

func (r *Repository) DeleteStepTemplate(
	ctx context.Context,
	templateID int64,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteTemplateVersionAgentSelectors(
			ctx,
			templateID,
		); err != nil {
			return err
		}
		return q.DeleteStepTemplate(ctx, templateID)
	})
}

func setTemplatePlacement(
	ctx context.Context,
	q *db.Queries,
	templateID int64,
	target string,
	selectors []string,
) error {
	changed, err := q.SetTemplateExecutionTarget(
		ctx,
		db.SetTemplateExecutionTargetParams{
			ExecutionTarget: target,
			ID:              templateID,
		},
	)
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	if err := q.DeleteTemplateAgentSelectors(ctx, templateID); err != nil {
		return err
	}
	if target != "agent" {
		return nil
	}
	for _, selector := range selectors {
		if err := q.AddTemplateAgentSelector(
			ctx,
			db.AddTemplateAgentSelectorParams{
				TemplateID: templateID,
				Label: strings.ToLower(
					strings.TrimSpace(selector),
				),
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func setTemplateVersionPlacement(
	ctx context.Context,
	q *db.Queries,
	versionID int64,
	target string,
	selectors []string,
) error {
	changed, err := q.SetTemplateVersionExecutionTarget(
		ctx,
		db.SetTemplateVersionExecutionTargetParams{
			ExecutionTarget: target,
			ID:              versionID,
		},
	)
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	if target != "agent" {
		return nil
	}
	for _, selector := range selectors {
		if err := q.AddTemplateVersionAgentSelector(
			ctx,
			db.AddTemplateVersionAgentSelectorParams{
				TemplateVersionID: versionID,
				Label: strings.ToLower(
					strings.TrimSpace(selector),
				),
			},
		); err != nil {
			return err
		}
	}
	return nil
}
