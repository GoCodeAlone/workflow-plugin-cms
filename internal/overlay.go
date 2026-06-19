package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

type OverlayStatus string

const (
	OverlayStatusDraft          OverlayStatus = "draft"
	OverlayStatusPublished      OverlayStatus = "published"
	OverlayStatusConflictReview OverlayStatus = "conflict_review"
	OverlayStatusDisabled       OverlayStatus = "disabled"
)

type OverlayMode string

const (
	OverlayModeReplace OverlayMode = "replace"
	OverlayModeAppend  OverlayMode = "append"
	OverlayModePrepend OverlayMode = "prepend"
	OverlayModeRemove  OverlayMode = "remove"
)

type OverlaySelector struct {
	Selector string      `json:"selector"`
	Mode     OverlayMode `json:"mode"`
}

type StaticPageOverlayInput struct {
	TenantID    int64             `json:"tenant_id"`
	SourcePath  string            `json:"source_path"`
	SourceHash  string            `json:"source_hash"`
	Selectors   []OverlaySelector `json:"selectors"`
	DraftBlocks json.RawMessage   `json:"draft_blocks"`
}

type StaticPageOverlay struct {
	TenantID    int64
	SourcePath  string
	SourceHash  string
	Selectors   []OverlaySelector
	DraftBlocks json.RawMessage
	Status      OverlayStatus
	Enabled     bool
}

type OverlayPublishResult struct {
	Published      bool
	ConflictReason string
	SourceHash     string
}

func NewStaticPageOverlay(input StaticPageOverlayInput) (*StaticPageOverlay, error) {
	sourcePath := cleanOverlaySourcePath(input.SourcePath)
	if input.TenantID == 0 {
		return nil, errors.New("overlay: tenant_id required")
	}
	if sourcePath == "" {
		return nil, errors.New("overlay: source_path required")
	}
	if strings.TrimSpace(input.SourceHash) == "" {
		return nil, errors.New("overlay: source_hash required")
	}
	if len(input.Selectors) == 0 {
		return nil, errors.New("overlay: at least one selector required")
	}
	for _, selector := range input.Selectors {
		if err := validateOverlaySelector(selector); err != nil {
			return nil, err
		}
	}
	return &StaticPageOverlay{
		TenantID:    input.TenantID,
		SourcePath:  sourcePath,
		SourceHash:  strings.TrimSpace(input.SourceHash),
		Selectors:   append([]OverlaySelector(nil), input.Selectors...),
		DraftBlocks: append(json.RawMessage(nil), input.DraftBlocks...),
		Status:      OverlayStatusDraft,
		Enabled:     true,
	}, nil
}

func PublishOverlay(overlay *StaticPageOverlay, currentSourceHash string, force bool) (OverlayPublishResult, error) {
	if overlay == nil {
		return OverlayPublishResult{}, errors.New("overlay: nil")
	}
	currentSourceHash = strings.TrimSpace(currentSourceHash)
	if currentSourceHash == "" {
		return OverlayPublishResult{}, errors.New("overlay: current_source_hash required")
	}
	if overlay.SourceHash != currentSourceHash && !force {
		overlay.Status = OverlayStatusConflictReview
		return OverlayPublishResult{
			Published:      false,
			ConflictReason: fmt.Sprintf("source hash changed from %s to %s", overlay.SourceHash, currentSourceHash),
			SourceHash:     overlay.SourceHash,
		}, nil
	}
	overlay.SourceHash = currentSourceHash
	overlay.Status = OverlayStatusPublished
	overlay.Enabled = true
	return OverlayPublishResult{Published: true, SourceHash: overlay.SourceHash}, nil
}

func DisableOverlay(overlay *StaticPageOverlay) {
	if overlay == nil {
		return
	}
	overlay.Enabled = false
	overlay.Status = OverlayStatusDisabled
}

func OverlayActiveForRender(overlay *StaticPageOverlay) bool {
	return overlay != nil && overlay.Enabled && overlay.Status == OverlayStatusPublished
}

func cleanOverlaySourcePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || strings.Contains(cleaned, "/../") || strings.HasPrefix(cleaned, "/..") {
		return ""
	}
	return cleaned
}

func validateOverlaySelector(selector OverlaySelector) error {
	if strings.TrimSpace(selector.Selector) == "" {
		return errors.New("overlay: selector required")
	}
	switch selector.Mode {
	case OverlayModeReplace, OverlayModeAppend, OverlayModePrepend, OverlayModeRemove:
		return nil
	default:
		return fmt.Errorf("overlay: invalid selector mode %q", selector.Mode)
	}
}
