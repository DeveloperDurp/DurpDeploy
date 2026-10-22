package api

// swagger:model ProjectGroupListResponse
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupListResponse struct {
	Items []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		ProjectCount int64  `json:"project_count"`
	} `json:"items"`
	Total  int64 `json:"total"`
	Limit  int64 `json:"limit"`
	Offset int64 `json:"offset"`
}

// swagger:parameters updateProjectGroup deleteProjectGroup moveProjectGroup
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupPathParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters createProjectGroup updateProjectGroup
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupBodyParam struct {
	// in: body
	// required: true
	Body projectGroupRequest
}

// swagger:parameters moveProjectGroup
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupAssignmentParam struct {
	// in: body
	// required: true
	Body projectGroupAssignment
}

// swagger:parameters listProjectGroups
//
//nolint:unused // Used by Swagger specification generation.
type projectGroupListParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}
