package internal

// AdminContribution describes a CMS admin surface in the shape expected by
// workflow-plugin-admin, without depending on that plugin's generated types.
type AdminContribution struct {
	ID          string
	Title       string
	Category    string
	Path        string
	RenderMode  string
	AppContext  string
	Permissions []string
	Metadata    map[string]string
	Actions     []string
}

// CMSAdminContribution returns the site-management admin surface metadata.
func CMSAdminContribution() AdminContribution {
	return AdminContribution{
		ID:         "cms-site-manager",
		Title:      "Sites",
		Category:   "content",
		Path:       "/admin/cms/sites",
		RenderMode: "iframe",
		AppContext: "multisite",
		Permissions: []string{
			"admin:multisite.sites:read",
			"admin:multisite.sites:update",
			"admin:multisite.pages:read",
			"admin:multisite.pages:update",
			"admin:multisite.publish:update",
			"admin:multisite.onboarding:plan",
		},
		Metadata: CMSAdminContributionMetadata(true),
		Actions: []string{
			"site.list",
			"domain.list",
			"page.list",
			"template.list",
			"overlay.list",
			"edit.launch",
		},
	}
}

// CMSAdminContributionMetadata returns API routes only after the host has
// already established admin authorization for this contribution.
func CMSAdminContributionMetadata(authorized bool) map[string]string {
	if !authorized {
		return map[string]string{}
	}
	return map[string]string{
		"sites_path":       "/api/v1/admin/tenants",
		"domains_path":     "/api/v1/admin/tenants/{tenant_id}/domains",
		"pages_path":       "/api/v1/admin/tenants/{tenant_id}/pages",
		"templates_path":   "/api/v1/admin/tenants/{tenant_id}/templates",
		"overlays_path":    "/api/v1/admin/tenants/{tenant_id}/overlays",
		"launch_edit_path": "/api/v1/admin/tenants/{tenant_id}/edit-session",
	}
}
