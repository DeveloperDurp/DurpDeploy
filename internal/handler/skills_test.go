package handler_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/handler"
	"durpdeploy/skills"
)

func newSkillsRouter() http.Handler {
	r := chi.NewRouter()
	h := handler.NewSkillsHandler()
	r.Get("/.well-known/skills/index.json", h.IndexV0x1)
	r.Get("/.well-known/skills/durpdeploy/SKILL.md", h.ServeSkill)
	r.Get("/.well-known/agent-skills/index.json", h.IndexV2)
	r.Get("/.well-known/agent-skills/durpdeploy/SKILL.md", h.ServeSkill)
	r.Head("/.well-known/skills/index.json", h.IndexV0x1)
	r.Head("/.well-known/skills/durpdeploy/SKILL.md", h.ServeSkill)
	r.Head("/.well-known/agent-skills/index.json", h.IndexV2)
	r.Head("/.well-known/agent-skills/durpdeploy/SKILL.md", h.ServeSkill)
	return r
}

func TestSkills_IndexV0x1(t *testing.T) {
	rec := httptest.NewRecorder()
	newSkillsRouter().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/.well-known/skills/index.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var body struct {
		Schema string `json:"$schema"`
		Skills []struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Files       []string `json:"files"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Schema != "" {
		t.Fatalf("v0.1 index must not have $schema, got %q", body.Schema)
	}
	if len(body.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(body.Skills))
	}
	s := body.Skills[0]
	if s.Name != "durpdeploy" {
		t.Fatalf("expected name durpdeploy, got %q", s.Name)
	}
	if s.Description != skills.SkillDescription {
		t.Fatalf("description mismatch: %q", s.Description)
	}
	if len(s.Files) != 1 || s.Files[0] != "durpdeploy/SKILL.md" {
		t.Fatalf("expected files [durpdeploy/SKILL.md], got %v", s.Files)
	}
}

func TestSkills_IndexV2(t *testing.T) {
	rec := httptest.NewRecorder()
	newSkillsRouter().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/.well-known/agent-skills/index.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Schema string `json:"$schema"`
		Skills []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Description string `json:"description"`
			URL         string `json:"url"`
			Digest      string `json:"digest"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Schema !=
		"https://schemas.agentskills.io/discovery/0.2.0/schema.json" {
		t.Fatalf("unexpected $schema: %q", body.Schema)
	}
	if len(body.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(body.Skills))
	}
	s := body.Skills[0]
	if s.Name != "durpdeploy" || s.Type != "skill-md" {
		t.Fatalf("unexpected skill entry: %q %q", s.Name, s.Type)
	}
	if s.URL != "/.well-known/agent-skills/durpdeploy/SKILL.md" {
		t.Fatalf("unexpected url: %q", s.URL)
	}
	sum := sha256.Sum256([]byte(skills.SkillMD))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if s.Digest != want {
		t.Fatalf("digest mismatch: got %q want %q", s.Digest, want)
	}
}

func TestSkills_ServeSkill(t *testing.T) {
	for _, path := range []string{
		"/.well-known/skills/durpdeploy/SKILL.md",
		"/.well-known/agent-skills/durpdeploy/SKILL.md",
	} {
		rec := httptest.NewRecorder()
		newSkillsRouter().ServeHTTP(rec, httptest.NewRequest(
			http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct !=
			"text/markdown; charset=utf-8" {
			t.Fatalf("%s: unexpected content type %q", path, ct)
		}
		got, err := io.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("%s: read body: %v", path, err)
		}
		if string(got) != skills.SkillMD {
			t.Fatalf("%s: body does not match skills.SkillMD", path)
		}
	}
}

func TestSkills_Head(t *testing.T) {
	for _, path := range []string{
		"/.well-known/skills/index.json",
		"/.well-known/skills/durpdeploy/SKILL.md",
		"/.well-known/agent-skills/index.json",
		"/.well-known/agent-skills/durpdeploy/SKILL.md",
	} {
		rec := httptest.NewRecorder()
		newSkillsRouter().ServeHTTP(rec, httptest.NewRequest(
			http.MethodHead, path, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if body, _ := io.ReadAll(rec.Body); len(body) != 0 {
			t.Fatalf("%s: HEAD must have empty body, got %d bytes",
				path, len(body))
		}
	}
}

func TestSkills_UnknownSkill404(t *testing.T) {
	rec := httptest.NewRecorder()
	newSkillsRouter().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/.well-known/skills/other/SKILL.md", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("expected not-found body, got %q", rec.Body.String())
	}
}
