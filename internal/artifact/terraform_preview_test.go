package artifact

import (
	"io"
	"runtime"
	"strings"
	"testing"
)

func TestTerraformPreviewLargeSelectedValueAllocations(t *testing.T) {
	const padding = 32 << 20
	reader := io.MultiReader(
		strings.NewReader(
			`{"format_version":"1.2","resource_changes":[{"change":{"actions":["create"],"after":"`,
		),
		&reviewPaddingReader{remaining: padding},
		strings.NewReader(`","after_sensitive":false}}]}`),
	)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	changes, err := TerraformReviewReader(reader, nil)
	runtime.ReadMemStats(&after)
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes=%d err=%v", len(changes), err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > padding/4 {
		t.Fatalf("oversized value allocated %d bytes", allocated)
	} else {
		t.Logf("32 MiB selected value allocated %d bytes", allocated)
	}
	if !strings.Contains(changes[0].After, "[OMITTED FROM PREVIEW]") {
		t.Fatal("oversized value was not explicitly omitted")
	}
}

func TestTerraformPreviewMasksAndAddressOmission(t *testing.T) {
	large := `"` + strings.Repeat("private-content", 6000) + `"`
	for _, field := range []string{"before", "after", "before_sensitive", "after_sensitive", "after_unknown"} {
		raw := `{"format_version":"1.2","resource_changes":[{"address":"example.test","change":{"actions":["create"],"before":"private-content","after":"private-content","before_sensitive":true,"after_sensitive":true,"` + field + `":` + large + `}}]}`
		preview, err := TerraformPreviewReader(strings.NewReader(raw), nil)
		if err != nil || !preview.PreviewTruncated ||
			len(preview.Resources) != 1 {
			t.Fatalf("field=%s preview=%+v err=%v", field, preview, err)
		}
		if strings.Contains(
			preview.Resources[0].Before+preview.Resources[0].After,
			"private-content",
		) {
			t.Fatalf("field=%s exposed hidden content", field)
		}
	}
	preview, err := TerraformPreviewReader(
		strings.NewReader(
			`{"format_version":"1.2","resource_changes":[{"address":`+large+`,"address":null}]}`,
		),
		nil,
	)
	if err != nil || !preview.PreviewTruncated ||
		preview.Resources[0].Address != terraformOmitted {
		t.Fatalf("oversized address/null preview=%+v err=%v", preview, err)
	}
}

func TestTerraformPreviewCountsAllOmittedResourcesAndActions(t *testing.T) {
	resource := `{"change":{"actions":[` + strings.TrimSuffix(
		strings.Repeat(`"create",`, 20),
		",",
	) + `]}}`
	raw := `{"format_version":"1.2","resource_changes":[` + strings.TrimSuffix(
		strings.Repeat(resource+",", 110),
		",",
	) + `]}`
	counts, err := ParseGateReview([]byte(raw), "terraform")
	if err != nil || counts.Create != 2200 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
	preview, err := TerraformPreviewReader(strings.NewReader(raw), nil)
	if err != nil || !preview.PreviewTruncated ||
		preview.ResourcesOmitted != 10 ||
		len(preview.Resources) != 100 ||
		len(preview.Resources[0].Actions) != 16 {
		t.Fatalf(
			"resources=%d omitted=%d err=%v",
			len(preview.Resources),
			preview.ResourcesOmitted,
			err,
		)
	}
}

func TestTerraformPreviewOverwrittenOmissionAndSkippedSyntax(t *testing.T) {
	large := `"` + strings.Repeat("x", previewValueBytes+1) + `"`
	for _, raw := range []string{
		`{"format_version":"1.2","resource_changes":[{"change":{"after":` + large + `,"after":"shown","after_sensitive":false}}]}`,
		`{"format_version":"1.2","resource_changes":[{"change":{"after":` + large + `}}],"resource_changes":[]}`,
	} {
		preview, err := TerraformPreviewReader(strings.NewReader(raw), nil)
		if err != nil || preview.PreviewTruncated ||
			preview.ResourcesOmitted != 0 {
			t.Fatalf("stale omission preview=%+v err=%v", preview, err)
		}
	}
	raw := `{"format_version":"1.2","resource_changes":[{"change":{"after":"` + strings.Repeat(
		"x",
		previewValueBytes+1,
	) + `\q"}}]}`
	if _, err := TerraformPreviewReader(strings.NewReader(raw), nil); err == nil {
		t.Fatal("omitted value hid malformed JSON")
	}
}

func TestTerraformPreviewDepthAndAggregateBudget(t *testing.T) {
	deep := strings.Repeat(
		"[",
		100,
	) + `"private-content"` + strings.Repeat(
		"]",
		100,
	)
	preview, err := TerraformPreviewReader(
		strings.NewReader(
			`{"format_version":"1.2","resource_changes":[{"change":{"after":`+deep+`,"after_sensitive":false}}]}`,
		),
		nil,
	)
	if err != nil || !preview.PreviewTruncated ||
		preview.Resources[0].After != terraformOmitted {
		t.Fatalf("deep preview=%+v err=%v", preview, err)
	}
	resource := `{"change":{"after":"` + strings.Repeat(
		"x",
		60000,
	) + `","after_sensitive":false}}`
	raw := `{"format_version":"1.2","resource_changes":[` + strings.TrimSuffix(
		strings.Repeat(resource+",", 30),
		",",
	) + `]}`
	preview, err = TerraformPreviewReader(strings.NewReader(raw), nil)
	if err != nil || !preview.PreviewTruncated ||
		preview.ResourcesOmitted == 0 {
		t.Fatalf(
			"aggregate resources=%d omitted=%d err=%v",
			len(preview.Resources),
			preview.ResourcesOmitted,
			err,
		)
	}
	var size int
	for _, change := range preview.Resources {
		size += len(change.Address) + len(change.Before) + len(change.After)
	}
	if size > previewTextBytes {
		t.Fatalf("preview budget exceeded: %d", size)
	}
}
