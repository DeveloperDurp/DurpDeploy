package api

// swagger:parameters getPackageRepository removePackageRepository
type swaggerArtifactProjectParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters savePackageRepository
type swaggerArtifactSaveParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body packageRepositoryRequest
}

// swagger:parameters testPackageRepository
type swaggerArtifactTestParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body packageTestRequest
}

// swagger:parameters getReleaseArtifact
type swaggerArtifactPinParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: path
	// required: true
	RelID int64 `json:"relId"`
}
