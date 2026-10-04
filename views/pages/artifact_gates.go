package pages

import (
	"encoding/json"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

type ArtifactGateInfo struct {
	DeploymentID   int64               `json:"deployment_id"`
	StepIndex      int64               `json:"step_index"`
	Revision       int64               `json:"revision"`
	Status         string              `json:"status"`
	SHA256         string              `json:"sha256"`
	BundleSHA256   string              `json:"bundle_sha256"`
	BundleSize     int64               `json:"bundle_size"`
	Review         artifact.GateReview `json:"review"`
	ReviewSource   string              `json:"review_source"`
	ReviewVerified bool                `json:"review_verified"`
	CreatedAt      int64               `json:"created_at"`
	ExpiresAt      int64               `json:"expires_at"`
	ApprovedBy     int64               `json:"approved_by,omitempty"`
	ApprovedAt     int64               `json:"approved_at,omitempty"`
}

func artifactGateBadge(status string) string {
	switch status {
	case "approved":
		return "badge-success"
	case "rejected", "expired", "cancelled":
		return "badge-error"
	default:
		return "badge-warning"
	}
}

func NewArtifactGateInfo(gate db.ArtifactGate) ArtifactGateInfo {
	var review artifact.GateReview
	if err := json.Unmarshal([]byte(gate.Review), &review); err != nil {
		review = artifact.GateReview{}
	}
	return ArtifactGateInfo{
		ReviewSource: "step_output",
		DeploymentID: gate.DeploymentID,
		StepIndex:    gate.StepIndex,
		Revision:     gate.Revision,
		Status:       gate.Status,
		SHA256:       gate.ArtifactSha256,
		BundleSHA256: gate.BundleSha256,
		BundleSize:   gate.BundleSize,
		Review:       review,
		CreatedAt:    gate.CreatedAt,
		ExpiresAt:    gate.ExpiresAt,
		ApprovedBy:   gate.ApprovedBy.Int64,
		ApprovedAt:   gate.ApprovedAt.Int64,
	}
}
