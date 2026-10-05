package api

import "durpdeploy/views/pages"
import "durpdeploy/internal/artifact"

// swagger:route GET /deployments/{id}/artifact-gates deployments listArtifactGates
//
// List checksum-bound artifact reviews. Sensitive contents are omitted.
//
// Security:
//   bearer:
// Responses:
//   200: body:ArtifactGateListResponse
//   403: body:ForbiddenError

// swagger:route GET /deployments/{id}/artifact-gates/{stepIndex}/artifact deployments downloadArtifactGate
//
// Download the exact sensitive artifact. Viewers cannot download artifacts.
//
// Produces:
// - application/octet-stream
// Security:
//   bearer:
// Responses:
//   200: description:Verified binary artifact
//   403: body:ForbiddenError
//   409: body:ConflictError

// swagger:route GET /deployments/{id}/artifact-gates/{stepIndex}/review deployments reviewArtifactGate
//
// Read redacted Terraform resource changes supplied by the step. Writers only.
// Review metadata is not independent verification of the saved plan.
//
// Security:
//   bearer:
// Responses:
//   200: ArtifactGateReviewResponse
//   403: body:ForbiddenError
//   409: body:ConflictError

// swagger:route POST /deployments/{id}/artifact-gates/{stepIndex}/approve deployments approveArtifactGate
//
// Approve this exact artifact revision and resume. Administrator only.
//
// Security:
//   bearer:
// Responses:
//   200: description:Decision accepted
//   403: body:ForbiddenError
//   409: body:ConflictError

// swagger:route POST /deployments/{id}/artifact-gates/{stepIndex}/reject deployments rejectArtifactGate
//
// Reject this exact artifact revision. Administrator only.
//
// Security:
//   bearer:
// Responses:
//   200: description:Decision accepted
//   403: body:ForbiddenError
//   409: body:ConflictError

// swagger:parameters listArtifactGates downloadArtifactGate reviewArtifactGate approveArtifactGate rejectArtifactGate
type artifactGatePathParam struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// swagger:parameters downloadArtifactGate reviewArtifactGate approveArtifactGate rejectArtifactGate
type artifactGateStepParam struct {
	// Zero-based generation step index.
	// in: path
	// required: true
	StepIndex int64 `json:"stepIndex"`
}

// swagger:parameters approveArtifactGate rejectArtifactGate
type artifactGateDecisionParam struct {
	// in: body
	// required: true
	Body struct {
		// required: true
		Revision int64 `json:"revision"`
		// required: true
		SHA256 string `json:"sha256"`
	}
}

// swagger:response ArtifactGateListResponse
type artifactGateListResponse struct {
	// in: body
	Body []pages.ArtifactGateInfo
}

// swagger:response ArtifactGateReviewResponse
type artifactGateReviewResponse struct {
	// in: body
	Body artifact.TerraformReviewResponse
}
