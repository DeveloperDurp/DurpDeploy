package httpstream

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestTextStreamEscapesEventLookingLines(t *testing.T) {
	// Given: log text includes SSE syntax and mixed newline encodings.
	line := "first\r\nevent: complete\rid: 99\n\ndata: end"
	var out bytes.Buffer
	// When: the text encoder emits the log.
	if err := writeStreamLog(
		&out,
		db.DeploymentLog{ID: 7, Line: line},
		"sse",
		true,
	); err != nil {
		t.Fatal(err)
	}
	// Then: every untrusted line is data, and only the real row provides an ID.
	for i, part := range strings.Split(strings.TrimSuffix(out.String(), "\n\n"), "\n") {
		if i == 0 {
			if part != "id: 7" {
				t.Fatal(part)
			}
			continue
		}
		if !strings.HasPrefix(part, "data: ") {
			t.Fatalf("injected SSE field: %q", part)
		}
	}
}

func TestLegacyReplayDoesNotAdvanceCursorOutOfOrder(t *testing.T) {
	// Given: sequence-ordered history has decreasing persisted IDs.
	var out bytes.Buffer
	// When: replay encodes the higher ID before the lower ID.
	for _, id := range []int64{9, 4} {
		if err := writeStreamLog(
			&out,
			db.DeploymentLog{ID: id, Line: "history"},
			"sse",
			false,
		); err != nil {
			t.Fatal(err)
		}
	}
	// Then: interrupted history does not advertise a cursor that skips a row.
	if strings.Contains(out.String(), "id:") {
		t.Fatalf("premature cursor: %s", out.String())
	}
}

func TestLiveNDJSONHandlesControlBytes(t *testing.T) {
	for _, line := range []string{"\x1b[31mhello\a", "quote\" slash\\", "invalid\xff"} {
		t.Run(line, func(t *testing.T) {
			// Given: control-bearing or invalid UTF-8 runtime output.
			var out bytes.Buffer
			// When: live NDJSON encodes it.
			if err := writeStreamLog(
				&out,
				db.DeploymentLog{Line: line},
				"ndjson",
				true,
			); err != nil {
				t.Fatal(err)
			}
			// Then: ordinary JSON clients can read the line with the legacy step field.
			var value struct {
				Line string
				Step string
			}
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value.Step != "" ||
				value.Line != strings.ToValidUTF8(line, "�") {
				t.Fatalf("decoded=%+v", value)
			}
		})
	}
}
