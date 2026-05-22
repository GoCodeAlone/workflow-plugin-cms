// Package postgres provides a Postgres-backed CMS store.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// Store implements store.TenantAdminStore and store.PageStore using Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a Postgres store using databaseURL.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres store: database url required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	s := NewWithPool(pool)
	if err := s.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// NewWithPool wraps an existing pgx pool.
func NewWithPool(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Close closes the owned pool.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Ping verifies DB reachability.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("postgres store: nil pool")
	}
	return s.pool.Ping(ctx)
}

// CreateTenant inserts a tenant.
func (s *Store) CreateTenant(ctx context.Context, t *store.Tenant) error {
	if t == nil {
		return errors.New("store: tenant required")
	}
	themeID, err := parseThemeID(t.ThemeID)
	if err != nil {
		return err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO tenants (slug, label, theme_id)
		VALUES ($1, $2, $3)
		RETURNING id, slug, label, COALESCE(theme_id::text, ''), created_at, updated_at
	`, strings.TrimSpace(t.Slug), strings.TrimSpace(t.Label), themeID)
	if err := scanTenant(row, t); err != nil {
		if isPGCode(err, pgUniqueViolation) {
			return store.ErrTenantSlugTaken
		}
		return err
	}
	return nil
}

// GetTenant returns a tenant by ID.
func (s *Store) GetTenant(ctx context.Context, id int64) (*store.Tenant, error) {
	t := &store.Tenant{}
	err := scanTenant(s.pool.QueryRow(ctx, `
		SELECT id, slug, label, COALESCE(theme_id::text, ''), created_at, updated_at
		FROM tenants
		WHERE id = $1
	`, id), t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateTenant updates a tenant.
func (s *Store) UpdateTenant(ctx context.Context, t *store.Tenant) error {
	if t == nil {
		return errors.New("store: tenant required")
	}
	themeID, err := parseThemeID(t.ThemeID)
	if err != nil {
		return err
	}
	err = scanTenant(s.pool.QueryRow(ctx, `
		UPDATE tenants
		SET slug = $2, label = $3, theme_id = $4, updated_at = now()
		WHERE id = $1
		RETURNING id, slug, label, COALESCE(theme_id::text, ''), created_at, updated_at
	`, t.ID, strings.TrimSpace(t.Slug), strings.TrimSpace(t.Label), themeID), t)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrTenantNotFound
	}
	if isPGCode(err, pgUniqueViolation) {
		return store.ErrTenantSlugTaken
	}
	return err
}

// DeleteTenant deletes a tenant.
func (s *Store) DeleteTenant(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrTenantNotFound
	}
	return nil
}

// ListTenants returns all tenants.
func (s *Store) ListTenants(ctx context.Context) ([]*store.Tenant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, slug, label, COALESCE(theme_id::text, ''), created_at, updated_at
		FROM tenants
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.Tenant
	for rows.Next() {
		t := &store.Tenant{}
		if err := scanTenant(rows, t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateDomain inserts a tenant domain.
func (s *Store) CreateDomain(ctx context.Context, d *store.Domain) error {
	if d == nil {
		return errors.New("store: domain required")
	}
	host := normalizeHost(d.Host)
	if host == "" {
		return errors.New("store: host required")
	}
	kind := strings.TrimSpace(d.Kind)
	if kind == "" {
		kind = "vanity"
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO domains (tenant_id, domain, kind, subsite_label)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id, tenant_id, domain, COALESCE(subsite_label, ''), kind
	`, d.TenantID, host, kind, strings.TrimSpace(d.SubsiteLabel))
	if err := scanDomain(row, d); err != nil {
		if isPGCode(err, pgUniqueViolation) {
			return store.ErrDomainTaken
		}
		if isPGCode(err, pgForeignKeyViolation) {
			return store.ErrTenantNotFound
		}
		return err
	}
	return nil
}

// DeleteDomain deletes a tenant-owned domain.
func (s *Store) DeleteDomain(ctx context.Context, tenantID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrDomainNotFound
	}
	return nil
}

// ListDomains returns domains for a tenant.
func (s *Store) ListDomains(ctx context.Context, tenantID int64) ([]*store.Domain, error) {
	exists, err := s.tenantExists(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, store.ErrTenantNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, domain, COALESCE(subsite_label, ''), kind
		FROM domains
		WHERE tenant_id = $1
		ORDER BY id
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.Domain
	for rows.Next() {
		d := &store.Domain{}
		if err := scanDomain(rows, d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Create inserts a page.
func (s *Store) Create(ctx context.Context, tenantID int64, p *store.Page) error {
	if p == nil {
		return errors.New("page: nil")
	}
	p.TenantID = tenantID
	p.Subsite = normalizeSubsite(p.Subsite)
	if err := p.Validate(); err != nil {
		return err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO pages (tenant_id, subsite, path, title, body_html, body_blocks, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, tenant_id, COALESCE(subsite, ''), path, title, COALESCE(body_html, ''),
			body_blocks, status, version, created_at, updated_at
	`, tenantID, p.Subsite, p.Path, p.Title, p.BodyHTML, nullableJSON(p.BodyBlocks), p.Status)
	if err := scanPage(row, p); err != nil {
		if isPGCode(err, pgUniqueViolation) {
			return store.ErrPathConflict
		}
		if isPGCode(err, pgForeignKeyViolation) {
			return store.ErrNotFound
		}
		return err
	}
	return nil
}

// Get returns a tenant-scoped page by ID.
func (s *Store) Get(ctx context.Context, tenantID int64, id int64) (*store.Page, error) {
	p := &store.Page{}
	err := scanPage(s.pool.QueryRow(ctx, pageSelectSQL()+` WHERE tenant_id = $1 AND id = $2`, tenantID, id), p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GetByPath returns a tenant-scoped page by subsite and path.
func (s *Store) GetByPath(ctx context.Context, tenantID int64, subsite, path string) (*store.Page, error) {
	p := &store.Page{}
	err := scanPage(s.pool.QueryRow(ctx, pageSelectSQL()+`
		WHERE tenant_id = $1 AND COALESCE(subsite, '') = $2 AND path = $3
	`, tenantID, normalizeSubsite(subsite), path), p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Update writes a tenant-scoped page.
func (s *Store) Update(ctx context.Context, tenantID int64, p *store.Page) error {
	if p == nil {
		return errors.New("page: nil")
	}
	p.TenantID = tenantID
	p.Subsite = normalizeSubsite(p.Subsite)
	if err := p.Validate(); err != nil {
		return err
	}
	err := scanPage(s.pool.QueryRow(ctx, `
		UPDATE pages
		SET subsite = $3, path = $4, title = $5, body_html = $6, body_blocks = $7,
			status = $8, version = version + 1, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING id, tenant_id, COALESCE(subsite, ''), path, title, COALESCE(body_html, ''),
			body_blocks, status, version, created_at, updated_at
	`, tenantID, p.ID, p.Subsite, p.Path, p.Title, p.BodyHTML, nullableJSON(p.BodyBlocks), p.Status), p)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if isPGCode(err, pgUniqueViolation) {
		return store.ErrPathConflict
	}
	return err
}

// Delete removes a tenant-scoped page.
func (s *Store) Delete(ctx context.Context, tenantID int64, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pages WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// List returns pages for a tenant, optionally filtered by subsite.
func (s *Store) List(ctx context.Context, tenantID int64, subsite string) ([]*store.Page, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if strings.TrimSpace(subsite) == "" {
		rows, err = s.pool.Query(ctx, pageSelectSQL()+` WHERE tenant_id = $1 ORDER BY path, id`, tenantID)
	} else {
		rows, err = s.pool.Query(ctx, pageSelectSQL()+`
			WHERE tenant_id = $1 AND COALESCE(subsite, '') = $2
			ORDER BY path, id
		`, tenantID, normalizeSubsite(subsite))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.Page
	for rows.Next() {
		p := &store.Page{}
		if err := scanPage(rows, p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) tenantExists(ctx context.Context, tenantID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, tenantID).Scan(&exists)
	return exists, err
}

func pageSelectSQL() string {
	return `SELECT id, tenant_id, COALESCE(subsite, ''), path, title, COALESCE(body_html, ''),
		body_blocks, status, version, created_at, updated_at FROM pages`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTenant(row scanner, t *store.Tenant) error {
	return row.Scan(&t.ID, &t.Slug, &t.Label, &t.ThemeID, &t.CreatedAt, &t.UpdatedAt)
}

func scanDomain(row scanner, d *store.Domain) error {
	return row.Scan(&d.ID, &d.TenantID, &d.Host, &d.SubsiteLabel, &d.Kind)
}

func scanPage(row scanner, p *store.Page) error {
	var (
		bodyBlocks []byte
		status     string
	)
	if err := row.Scan(
		&p.ID,
		&p.TenantID,
		&p.Subsite,
		&p.Path,
		&p.Title,
		&p.BodyHTML,
		&bodyBlocks,
		&status,
		&p.Version,
		&p.CreatedAt,
		&p.UpdatedAt,
	); err != nil {
		return err
	}
	p.BodyBlocks = bodyBlocks
	p.Status = store.PageStatus(status)
	return nil
}

func parseThemeID(value string) (sql.NullInt64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullInt64{}, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("store: theme_id must be numeric until theme CRUD lands")
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(host, ":")[0]))
}

func normalizeSubsite(subsite string) string {
	return strings.TrimSpace(subsite)
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func isPGCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

var (
	_ store.TenantAdminStore = (*Store)(nil)
	_ store.PageStore        = (*Store)(nil)
)
