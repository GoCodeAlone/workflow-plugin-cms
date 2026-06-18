package internal

import (
	"context"
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// engineModule is the CMS core: page CRUD + render + dynamic-section
// substitution + theme resolver + bundle fetcher + ingest webhook +
// upload handler.
type engineModule struct {
	name             string
	provider         string
	bundleStorage    string
	dbURL            string
	contentRepoToken string
	defaultThemeID   string
}

func newEngineModule(name string, config map[string]any) (sdk.ModuleInstance, error) {
	m := &engineModule{name: name}
	if v, ok := config["provider"].(string); ok {
		m.provider = v
	}
	if v, ok := config["bundle_storage"].(string); ok {
		m.bundleStorage = v
	}
	if v, ok := config["db_url"].(string); ok {
		m.dbURL = v
	}
	if v, ok := config["content_repo_token"].(string); ok {
		m.contentRepoToken = v
	}
	if v, ok := config["default_theme_id"].(string); ok {
		m.defaultThemeID = v
	}
	return m, nil
}

func (m *engineModule) Name() string { return m.name }

func (m *engineModule) Init() error                     { return nil }
func (m *engineModule) Start(ctx context.Context) error { return nil }
func (m *engineModule) Stop(ctx context.Context) error  { return nil }

func (m *engineModule) InvokeMethod(method string, args map[string]any) (map[string]any, error) {
	switch method {
	case "AdminContribution":
		authorized, _ := args["authorized"].(bool)
		contribution := CMSAdminContribution()
		contribution.Metadata = CMSAdminContributionMetadata(authorized)
		return map[string]any{"contribution": adminContributionMap(contribution)}, nil
	default:
		return nil, fmt.Errorf("cms.engine method %q is not supported", method)
	}
}

func adminContributionMap(contribution AdminContribution) map[string]any {
	return map[string]any{
		"id":          contribution.ID,
		"title":       contribution.Title,
		"category":    contribution.Category,
		"path":        contribution.Path,
		"render_mode": contribution.RenderMode,
		"app_context": contribution.AppContext,
		"permissions": contribution.Permissions,
		"metadata":    contribution.Metadata,
		"actions":     contribution.Actions,
	}
}
