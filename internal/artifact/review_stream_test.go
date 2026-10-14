package artifact

import (
	"io"
	"runtime"
	"strings"
	"testing"
)

type reviewPaddingReader struct{ remaining int64 }

func (r *reviewPaddingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	return int(n), nil
}

func paddedTerraformReview(size int64) io.Reader {
	return io.MultiReader(
		strings.NewReader(`{"prior_state":"`),
		&reviewPaddingReader{remaining: size},
		strings.NewReader(
			`","format_version":"1.2","resource_changes":[{"address":"example.item","change":{"actions":["create"],"after":{"name":"shown","password":"secret"},"after_sensitive":{"password":true}}}]}`,
		),
	)
}

func TestTerraformReviewIgnoredStateAllocations(t *testing.T) {
	// The source generates padding without retaining it in the test heap.
	const padding = 32 << 20
	for _, detailed := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "counts", true: "details"}[detailed],
			func(t *testing.T) {
				reader := paddedTerraformReview(padding)
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				var err error
				if detailed {
					var changes []TerraformResourceChange
					changes, err = TerraformReviewReader(reader, nil)
					if err == nil && (len(changes) != 1 ||
						!strings.Contains(changes[0].After, "shown") ||
						strings.Contains(changes[0].After, "secret")) {
						t.Fatal("review lost masking or resource data")
					}
				} else {
					var counts GateReview
					counts, err = ParseGateReviewReader(reader, "terraform")
					if err == nil && counts != (GateReview{Create: 1}) {
						t.Fatalf("counts=%+v", counts)
					}
				}
				runtime.ReadMemStats(&after)
				if err != nil {
					t.Fatal(err)
				}
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated > padding/4 {
					t.Fatalf(
						"ignored %d bytes allocated %d bytes",
						padding,
						allocated,
					)
				} else {
					t.Logf("ignored %d bytes allocated %d bytes", padding, allocated)
				}
			},
		)
	}
}

func TestTerraformReviewDuplicateFieldsAndNull(t *testing.T) {
	for _, raw := range []string{
		`{"format_version":"1.2","format_version":null,"resource_changes":[{"change":{"actions":["unknown"],"actions":["create"]}}]}`,
		`{"FORMAT_VERSION":"1.2","resource_changes":[{"change":{"actions":["delete"]},"change":null,"change":{"actions":["create"]}}]}`,
		`{"format_\u0076ersion":"1.2","resource_changes":[{"change":{"actions":["unknown"]}}],"resource_changes":[{"change":{"actions":["create"]}}]}`,
	} {
		counts, err := ParseGateReview([]byte(raw), "terraform")
		if err != nil || counts != (GateReview{Create: 1}) {
			t.Fatalf("counts=%+v err=%v input=%s", counts, err, raw)
		}
		changes, err := TerraformReview([]byte(raw), nil)
		if err != nil || len(changes) != 1 || len(changes[0].Actions) != 1 ||
			changes[0].Actions[0] != "create" {
			t.Fatalf("changes=%+v err=%v input=%s", changes, err, raw)
		}
	}
	if counts, err := ParseGateReview([]byte(`{"create":null,"create":1}`), "summary"); err != nil ||
		counts.Create != 1 {
		t.Fatalf("overwritten summary null: counts=%+v err=%v", counts, err)
	}
}

func TestTerraformReviewRejectsMalformedSkippedData(t *testing.T) {
	for _, suffix := range []string{
		`[1,]}`, `{"x":true false}}`, `"bad\q"}`, `01}`, `1e+}`, `null} {}`,
	} {
		raw := `{"format_version":"1.2","prior_state":` + suffix
		if _, err := ParseGateReview([]byte(raw), "terraform"); err == nil {
			t.Fatalf("capture accepted %s", raw)
		}
		if _, err := TerraformReview([]byte(raw), nil); err == nil {
			t.Fatalf("detail accepted %s", raw)
		}
	}
}

func TestTerraformReviewEmptyChangesRemainJSONArray(t *testing.T) {
	for _, resources := range []string{"", `,"resource_changes":[]`, `,"resource_changes":null`, `,"resource_changes":[{}],"resource_changes":[]`} {
		changes, err := TerraformReview(
			[]byte(`{"format_version":"1.2"`+resources+`}`),
			nil,
		)
		if err != nil || changes == nil || len(changes) != 0 {
			t.Fatalf("resources=%s changes=%+v err=%v", resources, changes, err)
		}
	}
}

func TestTerraformReviewEmptyActionsRemainJSONArray(t *testing.T) {
	changes, err := TerraformReview(
		[]byte(
			`{"format_version":"1.2","resource_changes":[{"change":{"actions":[]}}, {"change":{"actions":null}}, {}]}`,
		),
		nil,
	)
	if err != nil || len(changes) != 3 || changes[0].Actions == nil ||
		changes[1].Actions != nil ||
		changes[2].Actions != nil {
		t.Fatalf("changes=%+v err=%v", changes, err)
	}
}

func TestGateSummaryIntegerPaddingAndOverflow(t *testing.T) {
	counts, err := ParseGateReview(
		[]byte(`{"create":`+strings.Repeat(" ", 256)+`12}`),
		"summary",
	)
	if err != nil || counts.Create != 12 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
	for _, value := range []string{"9223372036854775808", "1" + strings.Repeat("0", 100), "1.0", "1e1", "true", `"12"`} {
		if _, err := ParseGateReview([]byte(`{"create":`+value+`}`), "summary"); err == nil {
			t.Fatalf("accepted invalid int64: %s", value)
		}
	}
}
