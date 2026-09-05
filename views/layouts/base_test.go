package layouts

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func Test_Base_advertises_favicon_in_document_head(t *testing.T) {
	// Given
	var output bytes.Buffer

	// When
	err := Base("DurpDeploy", "/login").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base layout: %v", err)
	}

	// Then
	const faviconLink = `<link rel="icon" type="image/png" sizes="64x64" href="/static/icons/favicon-64.png">`
	if !strings.Contains(output.String(), faviconLink) {
		t.Fatalf("base layout is missing favicon link %q", faviconLink)
	}
}
