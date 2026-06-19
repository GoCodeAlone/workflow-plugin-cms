package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	case "OverlayClone":
		input, err := overlayInputFromMap(args)
		if err != nil {
			return nil, err
		}
		overlay, err := NewStaticPageOverlay(input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"overlay": overlay}, nil
	case "OverlayPublish":
		overlay, err := overlayFromAny(args["overlay"])
		if err != nil {
			return nil, err
		}
		result, err := PublishOverlay(overlay, stringValue(args, "current_source_hash"), boolValue(args, "force"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"overlay": overlay, "result": result}, nil
	case "OverlayDisable":
		overlay, err := overlayFromAny(args["overlay"])
		if err != nil {
			return nil, err
		}
		DisableOverlay(overlay)
		return map[string]any{"overlay": overlay}, nil
	case "NavigationPublished":
		var body navigationBody
		if err := decodeMethodArgs(args, &body); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if body.Now != nil {
			now = body.Now.UTC()
		}
		items, err := PublishedNavigation(body.Items, now)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	case "WidgetRender":
		var body widgetRenderBody
		if err := decodeMethodArgs(args, &body); err != nil {
			return nil, err
		}
		html, err := RenderWidgetInstance(body.Instance, WidgetRegistry{Types: body.Types})
		if err != nil {
			return nil, err
		}
		return map[string]any{"html": html}, nil
	case "MediaValidate":
		var body mediaValidateBody
		if err := decodeMethodArgs(args, &body); err != nil {
			return nil, err
		}
		if err := ValidatePublishedMediaReference(body.Reference, MediaPolicy{AllowedObjectPrefixes: body.AllowedObjectPrefixes}); err != nil {
			return nil, err
		}
		return map[string]any{"valid": true}, nil
	default:
		return nil, fmt.Errorf("cms.engine method %q is not supported", method)
	}
}

func overlayInputFromMap(values map[string]any) (StaticPageOverlayInput, error) {
	var input StaticPageOverlayInput
	if err := decodeMethodArgs(values, &input); err != nil {
		return StaticPageOverlayInput{}, fmt.Errorf("overlay input decode: %w", err)
	}
	return input, nil
}

func decodeMethodArgs(values map[string]any, dst any) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("method args encode: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("method args decode: %w", err)
	}
	return nil
}

func overlayFromAny(value any) (*StaticPageOverlay, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("overlay encode: %w", err)
	}
	var overlay StaticPageOverlay
	if err := json.Unmarshal(raw, &overlay); err != nil {
		return nil, fmt.Errorf("overlay decode: %w", err)
	}
	return &overlay, nil
}

func boolValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
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
