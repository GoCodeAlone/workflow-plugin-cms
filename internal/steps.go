package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/GoCodeAlone/workflow-plugin-cms/bundle"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

type renderPageStep struct {
	name   string
	config map[string]any
}

func newRenderPageStep(name string, config map[string]any) sdk.StepInstance {
	return &renderPageStep{name: name, config: cloneMap(config)}
}

func (s *renderPageStep) Execute(_ context.Context, _ map[string]any, _ map[string]map[string]any, current map[string]any, _ map[string]any, config map[string]any) (*sdk.StepResult, error) {
	values := cloneMap(s.config)
	mergeMap(values, config)
	mergeMap(values, current)

	html := stringValue(values, "body_html", "html")
	if html == "" {
		return nil, errors.New("cms_render_page: body_html required")
	}
	contentType := stringValue(values, "content_type")
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}

	return &sdk.StepResult{Output: map[string]any{
		"rendered":     true,
		"html":         html,
		"title":        stringValue(values, "title"),
		"path":         stringValue(values, "path"),
		"tenant_id":    values["tenant_id"],
		"subsite":      stringValue(values, "subsite"),
		"content_type": contentType,
	}}, nil
}

type bundleActivateStep struct {
	name   string
	config map[string]any
}

func newBundleActivateStep(name string, config map[string]any) sdk.StepInstance {
	return &bundleActivateStep{name: name, config: cloneMap(config)}
}

func (s *bundleActivateStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, current map[string]any, _ map[string]any, config map[string]any) (*sdk.StepResult, error) {
	values := cloneMap(s.config)
	mergeMap(values, config)
	mergeMap(values, current)

	bundleRoot := stringValue(values, "bundle_root")
	tenantSlug := stringValue(values, "tenant_slug")
	version := stringValue(values, "version")
	tarballURL := stringValue(values, "tarball_url")
	token := stringValue(values, "token", "content_repo_token")
	sha, err := (&bundle.Fetcher{BundleRoot: bundleRoot, Token: token}).Activate(ctx, tenantSlug, version, tarballURL)
	if err != nil {
		return nil, fmt.Errorf("cms_bundle_activate: %w", err)
	}

	return &sdk.StepResult{Output: map[string]any{
		"activated":   true,
		"tenant_slug": tenantSlug,
		"version":     version,
		"sha256":      sha,
	}}, nil
}

func cloneMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeMap(dst map[string]any, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := values[key].(string); ok {
			return v
		}
	}
	return ""
}
