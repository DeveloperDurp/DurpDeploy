package api

import "durpdeploy/internal/repository"

// swagger:parameters listLibraryTemplates listProjectLibraryTemplates
//
//nolint:unused // Used by Swagger specification generation.
type libraryListQuery struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listProjectLibraryTemplates previewProjectLibraryTemplate applyLibraryTemplate
//
//nolint:unused // Used by Swagger specification generation.
type libraryProjectParam struct {
	// Destination project ID.
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters previewLibraryTemplate previewProjectLibraryTemplate applyLibraryTemplate
//
//nolint:unused // Used by Swagger specification generation.
type librarySlugParam struct {
	// Curated template slug from the library list.
	// in: path
	// required: true
	Slug string `json:"slug"`
}

// swagger:parameters applyLibraryTemplate
//
//nolint:unused // Used by Swagger specification generation.
type libraryApplyParam struct {
	// Supply the preview version, reviewed=true, and variable keys.
	// Host templates require agent labels. Server-container templates omit labels.
	// Every matching agent runs the steps. Use a unique label for a single host.
	// Terraform requires TF_CONTAINER_IMAGE and TF_CONFIG_DIR and pauses before apply.
	// in: body
	// required: true
	Body repository.ApplyLibraryTemplate
}
