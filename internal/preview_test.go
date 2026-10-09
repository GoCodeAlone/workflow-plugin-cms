package internal

import (
	"strings"
	"testing"
)

func TestPreviewCSPInsertedBeforeResourcesWithAttributedHead(t *testing.T) {
	doc := previewAssetPaths(`<html><head data-layout="x"><link href="https://external.test/style.css" rel="stylesheet"></head><body><img src="/assets/photo.jpg"></body></html>`, "/tenant-assets", "script-src 'none'; img-src 'self'")
	meta := strings.Index(doc, `http-equiv="Content-Security-Policy"`)
	resource := strings.Index(doc, "external.test")
	if meta < 0 || resource < meta || !strings.Contains(doc, `src="/tenant-assets/assets/photo.jpg"`) {
		t.Fatalf("preview CSP/assets missing: %s", doc)
	}
}
