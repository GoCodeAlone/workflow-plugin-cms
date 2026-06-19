package internal

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

type MediaPolicy struct {
	AllowedObjectPrefixes []string `json:"allowed_object_prefixes"`
}

func ValidatePublishedMediaReference(reference string, policy MediaPolicy) error {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return errors.New("media: reference required")
	}
	if strings.HasPrefix(reference, "//") {
		return errors.New("media: protocol-relative URLs are not allowed")
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return fmt.Errorf("media: invalid reference: %w", err)
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		return validateRelativeMediaReference(reference)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("media: only relative, http, and https references are supported")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, "wix") || strings.Contains(host, "parastorage") {
		return errors.New("media: source-host media URLs must be mirrored before publish")
	}
	for _, prefix := range policy.AllowedObjectPrefixes {
		if strings.HasPrefix(reference, prefix) {
			return nil
		}
	}
	return fmt.Errorf("media: URL %q is not owned by this site", reference)
}

func validateRelativeMediaReference(reference string) error {
	cleaned := path.Clean(reference)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return errors.New("media: relative reference must not escape site root")
	}
	return nil
}
