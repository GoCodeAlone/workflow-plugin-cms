package internal

import (
	"errors"
	"fmt"
	"strings"
)

type WidgetType struct {
	Type   string `json:"type"`
	Markup string `json:"markup"`
}

type WidgetRegistry struct {
	Types map[string]WidgetType `json:"types"`
}

type WidgetInstance struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func RenderWidgetInstance(instance WidgetInstance, registry WidgetRegistry) (string, error) {
	if strings.TrimSpace(instance.ID) == "" {
		return "", errors.New("widget: id required")
	}
	if err := ValidateWidgetRegistry(registry); err != nil {
		return "", err
	}
	typ, ok := registry.Types[instance.Type]
	if !ok {
		return "", fmt.Errorf("widget: type %q is not allowlisted", instance.Type)
	}
	return typ.Markup, nil
}

func ValidateWidgetRegistry(registry WidgetRegistry) error {
	if len(registry.Types) == 0 {
		return errors.New("widget: registry is empty")
	}
	for key, typ := range registry.Types {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(typ.Type) == "" {
			return errors.New("widget: type required")
		}
		if key != typ.Type {
			return fmt.Errorf("widget: registry key %q does not match type %q", key, typ.Type)
		}
		if unsafeWidgetMarkup(typ.Markup) {
			return fmt.Errorf("widget: type %q contains disallowed script markup", typ.Type)
		}
	}
	return nil
}

func unsafeWidgetMarkup(markup string) bool {
	lower := strings.ToLower(markup)
	return strings.Contains(lower, "<script") ||
		strings.Contains(lower, " onload=") ||
		strings.Contains(lower, " onerror=") ||
		strings.Contains(lower, " onclick=") ||
		strings.Contains(lower, "javascript:")
}
