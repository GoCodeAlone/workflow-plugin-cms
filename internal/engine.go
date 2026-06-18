package internal

import (
	"context"
	"encoding/json"
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
	default:
		return nil, fmt.Errorf("cms.engine method %q is not supported", method)
	}
}

func overlayInputFromMap(values map[string]any) (StaticPageOverlayInput, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return StaticPageOverlayInput{}, fmt.Errorf("overlay input encode: %w", err)
	}
	var input StaticPageOverlayInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return StaticPageOverlayInput{}, fmt.Errorf("overlay input decode: %w", err)
	}
	return input, nil
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
