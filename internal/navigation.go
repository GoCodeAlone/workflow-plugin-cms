package internal

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

type NavTargetKind string

const (
	NavTargetStatic   NavTargetKind = "static"
	NavTargetCMSPage  NavTargetKind = "cms_page"
	NavTargetOverlay  NavTargetKind = "overlay"
	NavTargetExternal NavTargetKind = "external"
)

type NavStatus string

const (
	NavStatusDraft     NavStatus = "draft"
	NavStatusPublished NavStatus = "published"
	NavStatusScheduled NavStatus = "scheduled"
	NavStatusArchived  NavStatus = "archived"
)

type NavigationItem struct {
	Label     string        `json:"label"`
	Kind      NavTargetKind `json:"kind"`
	Target    string        `json:"target"`
	Status    NavStatus     `json:"status"`
	PublishAt *time.Time    `json:"publish_at"`
}

func PublishedNavigation(items []NavigationItem, now time.Time) ([]NavigationItem, error) {
	out := make([]NavigationItem, 0, len(items))
	for _, item := range items {
		if err := ValidateNavigationItem(item); err != nil {
			return nil, err
		}
		if !navigationItemPublished(item, now) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func ValidateNavigationItem(item NavigationItem) error {
	if strings.TrimSpace(item.Label) == "" {
		return errors.New("navigation: label required")
	}
	if strings.TrimSpace(item.Target) == "" {
		return errors.New("navigation: target required")
	}
	switch item.Status {
	case "", NavStatusDraft, NavStatusPublished, NavStatusScheduled, NavStatusArchived:
	default:
		return fmt.Errorf("navigation: invalid status %q", item.Status)
	}
	switch item.Kind {
	case NavTargetStatic, NavTargetCMSPage, NavTargetOverlay:
		return validateSitePathTarget(item.Target)
	case NavTargetExternal:
		return validateExternalTarget(item.Target)
	default:
		return fmt.Errorf("navigation: invalid target kind %q", item.Kind)
	}
}

func navigationItemPublished(item NavigationItem, now time.Time) bool {
	switch item.Status {
	case NavStatusPublished:
		return true
	case NavStatusScheduled:
		return item.PublishAt != nil && !now.Before(item.PublishAt.UTC())
	default:
		return false
	}
}

func validateSitePathTarget(target string) error {
	if !strings.HasPrefix(target, "/") {
		return errors.New("navigation: site target must start with /")
	}
	pathPart := strings.SplitN(target, "#", 2)[0]
	cleaned := path.Clean(pathPart)
	if cleaned == "." || cleaned == "/" {
		return nil
	}
	if strings.HasPrefix(cleaned, "/..") {
		return errors.New("navigation: site target must not escape root")
	}
	return nil
}

func validateExternalTarget(target string) error {
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("navigation: invalid external URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("navigation: external target must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("navigation: external target host required")
	}
	return nil
}
