package api

// ReleaseSource contains optional, immutable public source identifiers.
// URL fields require HTTPS without userinfo, query parameters, or fragments.
// Fields accept at most 2048 bytes without whitespace or control characters.
// Credentials and recognized secret formats are rejected.
// swagger:model ReleaseSource
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReleaseSource struct {
	RepositoryURL string `json:"repository_url"`
	// Full hexadecimal SHA-1 or SHA-256 commit identifier.
	CommitSHA string `json:"commit_sha"`
	// Public HTTPS URL or artifact identifier, including OCI SHA-256 references.
	ArtifactRef string `json:"artifact_ref"`
	CIRunURL    string `json:"ci_run_url"`
}

// swagger:parameters createRelease createDeployment
//
//nolint:unused // Used by Swagger specification generation.
type creationIdempotencyParam struct {
	// Optional key containing 1 to 255 visible ASCII characters.
	// Keys are scoped to the user, project, and operation, with no expiry.
	// Matching retries return the original 201 response and Idempotency-Replayed: true.
	// Reuse with different input returns 409. Failed creations do not consume keys.
	// in: header
	Key string `json:"Idempotency-Key"`
}
