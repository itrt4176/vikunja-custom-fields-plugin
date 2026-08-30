package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"github.com/spf13/viper"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

// FieldConfig holds a field definition's scalar constraints. xorm serializes it
// to a JSON column (TEXT under sqlite, JSON/JSONB under mysql/postgres) — the
// same xorm:"json" mechanism api_tokens.APIPermissions uses. If the Task 1 spike
// chose text+manual, swap this tag to `xorm:"text null"` and marshal in the model
// methods (Task 7); the Go type stays the same.
type FieldConfig struct {
	Required  bool     `json:"required,omitempty"`
	Default   string   `json:"default,omitempty"`
	Min       *float64 `json:"min,omitempty"` // integer/decimal range; pointer so 0 ≠ unset
	Max       *float64 `json:"max,omitempty"`
	IsAPIOnly bool     `json:"is_api_only,omitempty"` // PRD stretch; S3 owns behavior
}

// CustomFieldDefinition is a single custom field's schema.
type CustomFieldDefinition struct {
	ID           int64       `xorm:"bigint autoincr not null unique pk" json:"id"`
	Name         string      `xorm:"varchar(255) not null" json:"name"`
	Type         string      `xorm:"varchar(50) not null" json:"type"`
	Description  string      `xorm:"varchar(500) null" json:"description,omitempty"`
	FieldConfig  FieldConfig `xorm:"json null" json:"field_config"`
	DisplayOrder int         `xorm:"int not null default 0" json:"display_order"`
	Created      time.Time   `xorm:"created not null" json:"-"`
	Updated      time.Time   `xorm:"updated not null" json:"-"`
}

func (CustomFieldDefinition) TableName() string { return "custom_field_definitions" }

// CustomFieldValue is one field's value on one task. S3 refines value typing and
// adds the UNIQUE(field, task) constraint and query indexes.
type CustomFieldValue struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null" json:"custom_field_definition_id"`
	TaskID                  int64     `xorm:"bigint not null" json:"task_id"`
	Value                   string    `xorm:"text" json:"value"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }

// CustomFieldOption is one row of a select/multiselect field's option list.
type CustomFieldOption struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null index" json:"custom_field_definition_id"`
	Value                   string    `xorm:"varchar(255) not null" json:"value"`
	Label                   string    `xorm:"varchar(255) null" json:"label,omitempty"`
	DisplayOrder            int       `xorm:"int not null default 0" json:"display_order"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldOption) TableName() string { return "custom_field_options" }

// CustomFieldProject assigns a field to a project. ProjectID 0 is the sentinel
// for "all projects"; a specific ID means that project only. The handler enforces
// that a field has either the 0-row or ≥1 specific rows, never both.
type CustomFieldProject struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null index" json:"custom_field_definition_id"`
	ProjectID               int64     `xorm:"bigint not null index" json:"project_id"`
	Created                 time.Time `xorm:"created not null" json:"-"`
}

func (CustomFieldProject) TableName() string { return "custom_field_projects" }

// whitelist holds the lowercase usernames permitted to manage custom fields.
// Populated once in Init() from Vikunja's config (customfields.whitelist,
// overridable by the VIKUNJA_CUSTOMFIELDS_WHITELIST env var); read-only
// afterward, so it needs no synchronization.
var whitelist map[string]struct{}

// loadWhitelist reads the management whitelist from Vikunja's config
// (the customfields.whitelist key, overridable by the VIKUNJA_CUSTOMFIELDS_WHITELIST
// env var) and returns a lowercase-normalized set of permitted usernames. Source
// is isolated here so a future swap to config.Key(...) is a one-function change.
//
// Malformed entries (empty after trimming, e.g. "alice,,bob") are logged and
// skipped — never fatal. An absent/empty value yields an empty set (deny-all).
func loadWhitelist() map[string]struct{} {
	set := map[string]struct{}{}
	raw := viper.GetString("customfields.whitelist")
	if raw == "" {
		log.Infof("[custom-fields] whitelist empty — no users may manage custom fields")
		return set
	}
	for i, entry := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(entry))
		if name == "" {
			log.Errorf("[custom-fields] whitelist: ignoring empty entry at position %d", i)
			continue
		}
		set[name] = struct{}{}
	}
	log.Infof("[custom-fields] whitelist loaded: %d manager(s)", len(set))
	return set
}

// IsManager reports whether username is on the management whitelist. It is the
// single authorization check S2 (field-definition API) and S9 (management UI)
// call before allowing field-definition changes. Deny-by-default: an empty
// whitelist denies everyone. Comparison is case-insensitive.
func IsManager(username string) bool {
	if username == "" {
		return false
	}
	_, ok := whitelist[strings.ToLower(username)]
	return ok
}

// CustomFieldsPlugin is the main plugin struct. All capabilities (tables, routes)
// are methods added to this struct in later tasks.
type CustomFieldsPlugin struct{}

func (p *CustomFieldsPlugin) Name() string    { return "custom-fields" }
func (p *CustomFieldsPlugin) Version() string { return "0.1.0" }

func (p *CustomFieldsPlugin) Init() error {
	whitelist = loadWhitelist()
	log.Infof("[custom-fields] plugin v0.1.0 initialized")
	return nil
}

func (p *CustomFieldsPlugin) Shutdown() error {
	log.Infof("[custom-fields] plugin shutting down")
	return nil
}

// Migrations creates the plugin's tables. Vikunja runs plugin migrations
// automatically on startup after core migrations and before Init().
//
// Yaegi interprets plugin structs as anonymous reflect structs with no methods,
// so a TableName() method is invisible to xorm and the table name must be passed
// explicitly via tx.Table(name).Sync2(&T{}) — not Sync2(new(T)), which would
// produce an empty table name and a SQL syntax error. See upstream PR #3549.
//
// This migration is modified in place across stories (pattern B: unreleased
// feature, project_views precedent) until the plugin runs in production, after
// which further schema changes become append-only new migrations.
func (p *CustomFieldsPlugin) Migrations() []*xormigrate.Migration {
	return []*xormigrate.Migration{{
		ID:          "20260829160000-create-custom-field-tables",
		Description: "Create custom field definition, value, option, and project-assignment tables",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Table("custom_field_definitions").Sync2(&CustomFieldDefinition{}); err != nil {
				return fmt.Errorf("custom-fields: sync definitions: %w", err)
			}
			if err := tx.Table("custom_field_values").Sync2(&CustomFieldValue{}); err != nil {
				return fmt.Errorf("custom-fields: sync values: %w", err)
			}
			if err := tx.Table("custom_field_options").Sync2(&CustomFieldOption{}); err != nil {
				return fmt.Errorf("custom-fields: sync options: %w", err)
			}
			if err := tx.Table("custom_field_projects").Sync2(&CustomFieldProject{}); err != nil {
				return fmt.Errorf("custom-fields: sync assignments: %w", err)
			}
			return nil
		},
		Rollback: func(tx *xorm.Engine) error {
			// Drop in dependency order: values + options + assignments reference definitions.
			return tx.DropTables("custom_field_values", "custom_field_options", "custom_field_projects", "custom_field_definitions")
		},
	}}
}

// RegisterAuthenticatedRoutes mounts the plugin's authenticated routes on the
// /api/v1/plugins/ group.
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)   // S1 throwaway load-proof
	g.GET("/custom-fields/manager", managerHandler) // S8 temporary, remove after S2
}

func healthHandler(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"name":    "custom-fields",
		"version": "0.1.0",
		"status":  "ok",
	})
}

// managerHandler is a temporary S8 verification route: it proves the whitelist
// predicate resolves correctly for the authenticated caller. It is not a
// management surface — S2/S9 enforce IsManager on the real endpoints. Remove
// this route once S2 is in place and the predicate is exercised there.
func managerHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"username":   u.Username,
		"is_manager": IsManager(u.Username),
	})
}

var singleton = &CustomFieldsPlugin{}

func NewPlugin() plugins.Plugin { return singleton }

// NewAuthenticatedRouterPlugin is the typed factory yaegi's loader requires — yaegi
// wraps return values per declared type, so sub-interface assertions don't work.
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return singleton }

// NewMigrationPlugin is the typed factory yaegi's loader looks for to register
// database migrations.
func NewMigrationPlugin() plugins.MigrationPlugin { return singleton }