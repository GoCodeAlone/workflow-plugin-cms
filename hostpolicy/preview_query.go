package hostpolicy

import (
	"net/url"
	"path"
	"strings"
)

func canonicalPreviewQueryTarget(target string) bool {
	if len(target) > 2048 {
		return false
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.Fragment != "" || u.RawFragment != "" || u.ForceQuery || u.String() != target || path.Clean(u.Path) != u.Path || !previewAccessPath(u) || u.RawQuery == "" || len(u.RawQuery) > 512 {
		return false
	}
	pairs := strings.Split(u.RawQuery, "&")
	if len(pairs) > 8 {
		return false
	}
	previous := ""
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || !previewQueryAtom(key, 64) || !previewQueryAtom(value, 128) || key <= previous {
			return false
		}
		previous = key
	}
	return true
}

func previewQueryAtom(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}
