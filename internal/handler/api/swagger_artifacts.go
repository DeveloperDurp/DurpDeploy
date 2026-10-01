package api

// swagger:model PackageRepositoryListResponse
type swaggerPackageRepositoryListResponse []packageRepositoryResponse

// swagger:parameters listPackageRepositories getArtifactRepository
type swaggerArtifactProjectParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters getPackageRepository deletePackageRepository
type swaggerArtifactRepositoryParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: path
	// required: true
	RepositoryID int64 `json:"repositoryId"`
}

// swagger:parameters createPackageRepository
type swaggerArtifactCreateParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body packageRepositoryRequest
}

// swagger:parameters updatePackageRepository
type swaggerArtifactUpdateParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: path
	// required: true
	RepositoryID int64 `json:"repositoryId"`
	// in: body
	// required: true
	Body packageRepositoryRequest
}

// swagger:parameters selectArtifactRepository
type swaggerArtifactSelectionParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: body
	// required: true
	Body artifactSelection
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
