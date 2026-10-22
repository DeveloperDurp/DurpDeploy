package provenance

import (
	"strings"
	"testing"
)

func TestSourceValidation(t *testing.T) {
	for _, source := range []Source{
		{},
		{RepositoryURL: "https://git.example.test/team/repo.git",
			CommitSHA:   strings.Repeat("a", 40),
			ArtifactRef: "registry.example.test/team/app@sha256:" + strings.Repeat("b", 64),
			CIRunURL:    "https://ci.example.test/builds/42"},
		{CommitSHA: strings.Repeat("F", 64), ArtifactRef: "release-v1.2.3.zip"},
	} {
		if err := source.Validate(); err != nil {
			t.Errorf("valid public provenance rejected: %v", err)
		}
	}
	for name, source := range map[string]Source{
		"userinfo":             {RepositoryURL: "https://user:credential@example.test/repo"},
		"signed URL":           {CIRunURL: "https://ci.example.test/job?token=credential"},
		"fragment":             {RepositoryURL: "https://example.test/repo#credential"},
		"scheme":               {CIRunURL: "javascript:alert(1)"},
		"short sha":            {CommitSHA: "abcdef"},
		"invalid sha":          {CommitSHA: strings.Repeat("z", 40)},
		"control":              {ArtifactRef: "artifact\npassword=value"},
		"oversized":            {ArtifactRef: strings.Repeat("x", 2049)},
		"invalid utf8":         {ArtifactRef: string([]byte{0xff})},
		"gitlab token":         {ArtifactRef: "glpat-1234567890abcdefghijkl"},
		"github token":         {ArtifactRef: "ghp_" + strings.Repeat("a", 36)},
		"application token":    {ArtifactRef: "ddp_pat_" + strings.Repeat("a", 64)},
		"CI token":             {ArtifactRef: "ghs_" + strings.Repeat("a", 36)},
		"JWT":                  {ArtifactRef: "eyJhbGciOiJIUzI1NiJ9.payload.signature"},
		"encoded token":        {CIRunURL: "https://ci.example.test/%67lpat-1234567890abcdefghijkl"},
		"artifact credentials": {ArtifactRef: "https://user:credential@example.test/artifact.zip"},
		"registry credentials": {ArtifactRef: "user:credential@registry.example.test/image"},
	} {
		t.Run(name, func(t *testing.T) {
			if source.Validate() == nil {
				t.Fatal("unsafe provenance accepted")
			}
		})
	}
}
