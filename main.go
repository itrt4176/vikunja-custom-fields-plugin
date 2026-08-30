package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
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

// ── Errors (plugin-local 9000s range; web.HTTPError is unavailable to yaegi,
// so handlers translate these to echo.NewHTTPError(code, message). Upstream
// conversion: replace echo.NewHTTPError with HTTPError()/ErrCode per custom-errors.md.)

const (
	ErrCodeCustomFieldNameEmpty            = 9001
	ErrCodeCustomFieldInvalidType          = 9002
	ErrCodeCustomFieldOptionsForNonSelect  = 9003
	ErrCodeCustomFieldDuplicateOption      = 9004
	ErrCodeCustomFieldConstraintForType    = 9005
	ErrCodeCustomFieldInvalidConstraint    = 9006
	ErrCodeCustomFieldProjectNotFound      = 9007
	ErrCodeCustomFieldNotFound             = 9008
	ErrCodeCustomFieldGlobalConflict       = 9009
)

type ErrCustomFieldNameEmpty struct{}
func (ErrCustomFieldNameEmpty) Error() string { return "custom field name must not be empty" }

type ErrCustomFieldInvalidType struct{ Type string }
func (e ErrCustomFieldInvalidType) Error() string {
	return fmt.Sprintf("invalid custom field type: %s", e.Type)
}

type ErrCustomFieldOptionsForNonSelect struct{ Type string }
func (e ErrCustomFieldOptionsForNonSelect) Error() string {
	return fmt.Sprintf("options are only allowed for select/multiselect, not %s", e.Type)
}

type ErrCustomFieldDuplicateOption struct{ Value string }
func (e ErrCustomFieldDuplicateOption) Error() string {
	return fmt.Sprintf("duplicate option value: %s", e.Value)
}

type ErrCustomFieldConstraintForType struct{ Type, Constraint string }
func (e ErrCustomFieldConstraintForType) Error() string {
	return fmt.Sprintf("constraint %s is not valid for type %s", e.Constraint, e.Type)
}

type ErrCustomFieldInvalidConstraint struct{ Detail string }
func (e ErrCustomFieldInvalidConstraint) Error() string {
	return fmt.Sprintf("invalid constraint: %s", e.Detail)
}

type ErrCustomFieldProjectNotFound struct{ ID int64 }
func (e ErrCustomFieldProjectNotFound) Error() string {
	return fmt.Sprintf("project %d does not exist", e.ID)
}

type ErrCustomFieldNotFound struct{ ID int64 }
func (e ErrCustomFieldNotFound) Error() string {
	return fmt.Sprintf("custom field definition %d not found", e.ID)
}

// ErrCustomFieldGlobalConflict: assignment mixes the global sentinel (project_id=0)
// with specific projects, or carries the sentinel alongside specific rows.
type ErrCustomFieldGlobalConflict struct{}
func (ErrCustomFieldGlobalConflict) Error() string {
	return "a field is either global (all projects) or assigned to specific projects, not both"
}

// ── Validation (pure functions; Task 4). Tasks 7 and 8 call these before
// writing a definition or its project assignments.

var validFieldTypes = map[string]struct{}{
	"text": {}, "textarea": {}, "integer": {}, "decimal": {},
	"date": {}, "datetime": {}, "select": {}, "multiselect": {},
	"checkbox": {}, "url": {},
}

func isSelectLike(t string) bool {
	return t == "select" || t == "multiselect"
}

// validateDefinition checks the type/name/options/constraints of a definition.
// It does no DB access. project assignment is validated separately
// (validateAssignment) because that needs a session.
func validateDefinition(d *CustomFieldDefinition, options []CustomFieldOption) error {
	if strings.TrimSpace(d.Name) == "" {
		return ErrCustomFieldNameEmpty{}
	}
	if _, ok := validFieldTypes[d.Type]; !ok {
		return ErrCustomFieldInvalidType{Type: d.Type}
	}
	if len(options) > 0 && !isSelectLike(d.Type) {
		return ErrCustomFieldOptionsForNonSelect{Type: d.Type}
	}
	seen := map[string]struct{}{}
	for _, o := range options {
		if strings.TrimSpace(o.Value) == "" {
			return ErrCustomFieldInvalidConstraint{Detail: "option value must not be empty"}
		}
		if _, dup := seen[o.Value]; dup {
			return ErrCustomFieldDuplicateOption{Value: o.Value}
		}
		seen[o.Value] = struct{}{}
	}
	if (d.FieldConfig.Min != nil || d.FieldConfig.Max != nil) && !(d.Type == "integer" || d.Type == "decimal") {
		return ErrCustomFieldConstraintForType{Type: d.Type, Constraint: "min/max"}
	}
	if d.FieldConfig.Min != nil && d.FieldConfig.Max != nil && *d.FieldConfig.Min > *d.FieldConfig.Max {
		return ErrCustomFieldInvalidConstraint{Detail: "min must not exceed max"}
	}
	return nil
}

// validateAssignment confirms each specific project exists. The global sentinel
// (empty/nil projectIDs) needs no such check. Mixing sentinel with specific IDs
// is a client error the handler prevents before calling here.
func validateAssignment(s *xorm.Session, projectIDs []int64) error {
	for _, pid := range projectIDs {
		has, err := s.Table("projects").Where("id = ?", pid).Exist(&models.Project{})
		if err != nil {
			return fmt.Errorf("custom-fields: check project %d: %w", pid, err)
		}
		if !has {
			return ErrCustomFieldProjectNotFound{ID: pid}
		}
	}
	return nil
}

// ── Permissions. All field-definition management is whitelist-gated. These
// mirror Vikunja's Permissions interface (CanCreate/CanRead/CanUpdate/CanDelete);
// the only deviation is *user.User instead of web.Auth (web is unavailable to
// yaegi) — an upstream-conversion point.

func (d *CustomFieldDefinition) CanCreate(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanRead(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanUpdate(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanDelete(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

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

// ── Model CRUD. Handlers (Task 8) own the session: open, call CanX, call these,
// commit, close. These methods never open/commit the session themselves. No events
// (deferred — see spec Events section).

// resolveProjectIDs enforces mutual exclusivity: empty ⟹ global sentinel [0];
// non-empty ⟹ those IDs. The caller must not pass both a sentinel and specifics.
func resolveProjectIDs(projectIDs []int64) []int64 {
	if len(projectIDs) == 0 {
		return []int64{0} // sentinel: all projects
	}
	return projectIDs
}

// setOptions replaces a definition's option rows. delete-existing is a no-op on
// Create (none exist yet); on Update it clears the old set before re-inserting.
// Options are only written for select/multiselect.
func setOptions(s *xorm.Session, defID int64, t string, options []CustomFieldOption) error {
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", defID).Delete(&CustomFieldOption{}); err != nil {
		return fmt.Errorf("custom-fields: clear options: %w", err)
	}
	if !isSelectLike(t) || len(options) == 0 {
		return nil
	}
	for i := range options {
		options[i].CustomFieldDefinitionID = defID
	}
	if _, err := s.Table("custom_field_options").Insert(&options); err != nil {
		return fmt.Errorf("custom-fields: insert options: %w", err)
	}
	return nil
}

// setAssignment replaces a definition's project assignment. delete-existing is a
// no-op on Create; on Update it clears the old set before re-inserting.
func setAssignment(s *xorm.Session, defID int64, projectIDs []int64) error {
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", defID).Delete(&CustomFieldProject{}); err != nil {
		return fmt.Errorf("custom-fields: clear assignment: %w", err)
	}
	assign := resolveProjectIDs(projectIDs)
	rows := make([]CustomFieldProject, len(assign))
	for i, pid := range assign {
		rows[i] = CustomFieldProject{CustomFieldDefinitionID: defID, ProjectID: pid}
	}
	if _, err := s.Table("custom_field_projects").Insert(&rows); err != nil {
		return fmt.Errorf("custom-fields: insert assignment: %w", err)
	}
	return nil
}

func (d *CustomFieldDefinition) Create(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error) {
	if err := validateDefinition(d, options); err != nil {
		return nil, err
	}
	if err := validateAssignment(s, projectIDs); err != nil {
		return nil, err
	}
	if _, err := s.Table("custom_field_definitions").Insert(d); err != nil {
		return nil, fmt.Errorf("custom-fields: insert definition: %w", err)
	}
	if err := setOptions(s, d.ID, d.Type, options); err != nil {
		return nil, err
	}
	if err := setAssignment(s, d.ID, projectIDs); err != nil {
		return nil, err
	}
	return d, nil
}

// ReadOne fetches a definition with its options and project assignment. Returns
// the definition, its options (empty for non-select), and its project_ids (empty
// slice if global — callers treat empty as "all projects").
func (d *CustomFieldDefinition) ReadOne(s *xorm.Session) (*CustomFieldDefinition, []CustomFieldOption, []int64, error) {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Get(d)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get definition: %w", err)
	}
	if !has {
		return nil, nil, nil, ErrCustomFieldNotFound{ID: d.ID}
	}
	var opts []CustomFieldOption
	if err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).OrderBy("display_order asc").Find(&opts); err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get options: %w", err)
	}
	var assigns []CustomFieldProject
	if err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Find(&assigns); err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get assignment: %w", err)
	}
	pids := make([]int64, 0, len(assigns))
	for _, a := range assigns {
		if a.ProjectID != 0 { // omit the global sentinel from the response list
			pids = append(pids, a.ProjectID)
		}
	}
	return d, opts, pids, nil
}

// ReadAll lists all definitions. If projectID > 0, filters to fields that apply
// to that project (global sentinel OR a row for that project).
func ReadAll(s *xorm.Session, projectID int64) ([]CustomFieldDefinition, error) {
	var defs []CustomFieldDefinition
	if projectID == 0 {
		if err := s.Table("custom_field_definitions").OrderBy("display_order asc").Find(&defs); err != nil {
			return nil, fmt.Errorf("custom-fields: list definitions: %w", err)
		}
		return defs, nil
	}
	// Fields applying to projectID: those with a custom_field_projects row where
	// project_id = projectID OR project_id = 0 (global).
	subQuery := "(SELECT DISTINCT custom_field_definition_id FROM custom_field_projects WHERE project_id = ? OR project_id = 0)"
	if err := s.Table("custom_field_definitions").Where("id IN "+subQuery, projectID).OrderBy("display_order asc").Find(&defs); err != nil {
		return nil, fmt.Errorf("custom-fields: list definitions by project: %w", err)
	}
	return defs, nil
}

// Update replaces the definition and its options + assignment wholesale (PUT
// full-replace). It does NOT touch custom_field_values (S3's table). No event
// (deferred). The handler captures no `old` state.
func (d *CustomFieldDefinition) Update(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error) {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Exist(&CustomFieldDefinition{})
	if err != nil {
		return nil, fmt.Errorf("custom-fields: check old definition: %w", err)
	}
	if !has {
		return nil, ErrCustomFieldNotFound{ID: d.ID}
	}
	if err := validateDefinition(d, options); err != nil {
		return nil, err
	}
	if err := validateAssignment(s, projectIDs); err != nil {
		return nil, err
	}
	// AllCols writes every column including zero values (xorm's Update skips
	// zero-valued cols by default, which would break PUT full-replace for
	// cleared fields, display_order=0, field_config.required=false, etc.).
	// UseBool ensures the bools inside FieldConfig are not skipped either.
	// (Mirrors upstream label.go's explicit .Cols(...) approach.)
	if _, err := s.Table("custom_field_definitions").ID(d.ID).AllCols().UseBool().Update(d); err != nil {
		return nil, fmt.Errorf("custom-fields: update definition: %w", err)
	}
	if err := setOptions(s, d.ID, d.Type, options); err != nil {
		return nil, err
	}
	if err := setAssignment(s, d.ID, projectIDs); err != nil {
		return nil, err
	}
	return d, nil
}

// Delete hard-cascades the definition's OWN rows: definition + options +
// assignment. It does NOT touch custom_field_values (S3's table). No event
// (deferred).
func (d *CustomFieldDefinition) Delete(s *xorm.Session) error {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Exist(&CustomFieldDefinition{})
	if err != nil {
		return fmt.Errorf("custom-fields: check definition: %w", err)
	}
	if !has {
		return ErrCustomFieldNotFound{ID: d.ID}
	}
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldOption{}); err != nil {
		return fmt.Errorf("custom-fields: delete options: %w", err)
	}
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldProject{}); err != nil {
		return fmt.Errorf("custom-fields: delete assignment: %w", err)
	}
	if _, err := s.Table("custom_field_definitions").ID(d.ID).Delete(&CustomFieldDefinition{}); err != nil {
		return fmt.Errorf("custom-fields: delete definition: %w", err)
	}
	return nil
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