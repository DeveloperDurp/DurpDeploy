package api

// swagger:parameters getPackageRepository removePackageRepository
//
//nolint:unused // Used by Swagger specification generation.
type swaggerArtifactProjectParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters savePackageRepository
//
//nolint:unused // Used by Swagger specification generation.
type swaggerArtifactSaveParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body packageRepositoryRequest
}

// swagger:parameters testPackageRepository
//
//nolint:unused // Used by Swagger specification generation.
type swaggerArtifactTestParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body packageTestRequest
}

// swagger:parameters getReleaseArtifact
//
//nolint:unused // Used by Swagger specification generation.
type swaggerArtifactPinParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: path
	// required: true
	RelID int64 `json:"relId"`
}
