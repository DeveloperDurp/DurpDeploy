package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"durpdeploy/skills"
)

type skillEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files,omitempty"`
	Type        string   `json:"type,omitempty"`
	URL         string   `json:"url,omitempty"`
	Digest      string   `json:"digest,omitempty"`
}

type skillIndex struct {
	Skills []skillEntry `json:"skills"`
}

type SkillsHandler struct {
	digest string
}

func NewSkillsHandler() *SkillsHandler {
	sum := sha256.Sum256([]byte(skills.SkillMD))
	return &SkillsHandler{digest: hex.EncodeToString(sum[:])}
}

// IndexV0x1 serves the v0.1-style discovery index (no $schema field).
func (h *SkillsHandler) IndexV0x1(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, skillIndex{
		Skills: []skillEntry{{
			Name:        skills.SkillName,
			Description: skills.SkillDescription,
			Files:       []string{skills.SkillName + "/SKILL.md"},
		}},
	})
}

// IndexV2 serves the Agent Skills discovery index, RFC v0.2.0.
func (h *SkillsHandler) IndexV2(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{
		"$schema": "https://schemas.agentskills.io/discovery/0.2.0/schema.json",
		"skills": []skillEntry{
			{
				Name:        skills.SkillName,
				Type:        "skill-md",
				Description: skills.SkillDescription,
				URL:         "/.well-known/agent-skills/" + skills.SkillName + "/SKILL.md",
				Digest:      "sha256:" + h.digest,
			},
		},
	})
}

// ServeSkill serves the raw SKILL.md file.
func (h *SkillsHandler) ServeSkill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(skills.SkillMD))
}

func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
