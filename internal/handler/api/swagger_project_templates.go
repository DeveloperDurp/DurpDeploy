package api

import "durpdeploy/internal/repository"

// swagger:parameters listProjectTemplates listSourceProjectTemplates
//
//nolint:unused // Used by Swagger specification generation.
type projectTemplateListQueryParams struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters saveProjectTemplate previewProjectTemplate applyProjectTemplate listSourceProjectTemplates
//
//nolint:unused // Used by Swagger specification generation.
type projectTemplateSourceParam struct {
	// Source project ID.
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters previewProjectTemplate applyProjectTemplate
//
//nolint:unused // Used by Swagger specification generation.
type projectTemplateIDParam struct {
	// Template ID within the source project.
	// in: path
	// required: true
	ID int64 `json:"templateId"`
}

// swagger:parameters saveProjectTemplate
//
//nolint:unused // Used by Swagger specification generation.
type saveProjectTemplateParam struct {
	// in: body
	// required: true
	Body saveProjectTemplateRequest
}

// swagger:parameters applyProjectTemplate
//
//nolint:unused // Used by Swagger specification generation.
type applyProjectTemplateParam struct {
	// Use snapshot variable keys for variable_values.
	// Use source environment IDs for environment_mappings.
	// Use one-based step positions for agent_selectors overrides.
	// Supply all secret values and package credentials independently.
	// Set reviewed to true after reviewing the preview.
	// in: body
	// required: true
	Body repository.ApplyProjectTemplate
}
