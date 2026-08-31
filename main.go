package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/db"
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
	CustomFieldDefinitionID int64     `xorm:"bigint not null unique(field_task)" json:"custom_field_definition_id"`
	TaskID                  int64     `xorm:"bigint not null unique(field_task) index" json:"task_id"`
	Value                   string    `xorm:"text" json:"value"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }

// CustomFieldValueOption is one selected option of a select/multiselect value on a task.
// The label_tasks shape: a real table with its own PK, FKs to the value and the option.
type CustomFieldValueOption struct {
	ID                  int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldValueID  int64     `xorm:"bigint not null index" json:"custom_field_value_id"`
	CustomFieldOptionID int64     `xorm:"bigint not null index" json:"custom_field_option_id"`
	Created             time.Time `xorm:"created not null" json:"-"`
}

func (CustomFieldValueOption) TableName() string { return "custom_field_value_options" }

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
	ErrCodeCustomFieldNameEmpty           = 9001
	ErrCodeCustomFieldInvalidType         = 9002
	ErrCodeCustomFieldOptionsForNonSelect = 9003
	ErrCodeCustomFieldDuplicateOption     = 9004
	ErrCodeCustomFieldConstraintForType   = 9005
	ErrCodeCustomFieldInvalidConstraint   = 9006
	ErrCodeCustomFieldProjectNotFound     = 9007
	ErrCodeCustomFieldNotFound            = 9008
	ErrCodeCustomFieldGlobalConflict      = 9009
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
		options[i].ID = 0 // don't let a client-supplied id reach Insert
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
		return nil, fmt.Errorf("custom-fields: check definition: %w", err)
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

// ── Handlers. Thin: parse → CanX (403) → model → commit → JSON map. No events
// (deferred). web.HTTPError is unavailable, so handlers use echo.NewHTTPError.
//
// R7 (spike 1 caveat): interpreted structs serialize as {} through c.JSON, so
// responses are built as maps field-by-field, never by echoing a struct. xorm
// DB read/write of interpreted structs works; only the c.JSON path is affected.

type definitionRequest struct {
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Description  string              `json:"description"`
	FieldConfig  FieldConfig         `json:"field_config"`
	DisplayOrder int                 `json:"display_order"`
	Options      []CustomFieldOption `json:"options"`
	ProjectIDs   []int64             `json:"project_ids"`
}

// fieldConfigMap builds the field_config map with concrete float64 values
// (dereferenced pointers) so c.JSON never has to marshal a yaegi-wrapped *float64.
func fieldConfigMap(fc FieldConfig) map[string]interface{} {
	m := map[string]interface{}{
		"required":    fc.Required,
		"default":     fc.Default,
		"is_api_only": fc.IsAPIOnly,
	}
	if fc.Min != nil {
		m["min"] = *fc.Min
	}
	if fc.Max != nil {
		m["max"] = *fc.Max
	}
	return m
}

// definitionFieldsMap is the definition fields only (for list items, no relations).
func definitionFieldsMap(d *CustomFieldDefinition) map[string]interface{} {
	return map[string]interface{}{
		"id":            d.ID,
		"name":          d.Name,
		"type":          d.Type,
		"description":   d.Description,
		"field_config":  fieldConfigMap(d.FieldConfig),
		"display_order": d.DisplayOrder,
	}
}

// definitionToMap is the full single-resource response: definition fields +
// resolved options + project_ids ([] for global). Used by create/read/update.
func definitionToMap(d *CustomFieldDefinition, opts []CustomFieldOption, pids []int64) map[string]interface{} {
	m := definitionFieldsMap(d)
	optMaps := make([]map[string]interface{}, 0, len(opts))
	for _, o := range opts {
		optMaps = append(optMaps, map[string]interface{}{
			"id":                         o.ID,
			"custom_field_definition_id": o.CustomFieldDefinitionID,
			"value":                      o.Value,
			"label":                      o.Label,
			"display_order":              o.DisplayOrder,
		})
	}
	m["options"] = optMaps
	m["project_ids"] = pids
	return m
}

// validateProjectIDList (R4) rejects a client-supplied project_id == 0 — the
// reserved internal sentinel. Clients express "all projects" via [] (omitted),
// never via [0]. This makes ErrCustomFieldGlobalConflict reachable and separates
// the mutual-exclusivity guard from validateAssignment's existence check.
func validateProjectIDList(ids []int64) error {
	for _, pid := range ids {
		if pid == 0 {
			return ErrCustomFieldGlobalConflict{}
		}
	}
	return nil
}

// toHTTPError translates plugin-local errors to echo HTTP errors. web.HTTPError
// is unavailable to yaegi, so this uses echo.NewHTTPError(code, message).
func toHTTPError(err error) error {
	msg := err.Error()
	// yaegi wraps interpreted errors as interp._error, so switch err.(type) never
	// matches — discriminate by message prefix instead. Messages are stable
	// constants from our error types; wrapped DB errors start with "custom-fields:"
	// and fall to the 500 default. Upstream conversion: revert to switch err.(type)
	// (type assertions work in native Go). Second yaegi deviation: a switch-true
	// case list ("case a, b, c:") evaluates only its first expression, so each
	// prefix gets its own case clause.
	switch {
	case strings.HasPrefix(msg, "custom field definition ") && strings.Contains(msg, " not found"):
		return echo.NewHTTPError(http.StatusNotFound, msg)
	case strings.HasPrefix(msg, "custom field name must not be empty"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "invalid custom field type:"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "options are only allowed for select/multiselect"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "duplicate option value:"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "constraint "):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "invalid constraint:"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "project ") && strings.Contains(msg, " does not exist"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "a field is either global"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, msg)
	}
}

func createHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateProjectIDList(req.ProjectIDs); err != nil { // R4: reject sentinel
		return toHTTPError(err)
	}
	d := &CustomFieldDefinition{
		Name: req.Name, Type: req.Type, Description: req.Description,
		FieldConfig: req.FieldConfig, DisplayOrder: req.DisplayOrder,
	}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanCreate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	created, err := d.Create(s, u, req.Options, req.ProjectIDs)
	if err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R5: re-read for a canonical response (real option IDs, resolved project_ids).
	rd := &CustomFieldDefinition{ID: created.ID}
	def, opts, pids, err := rd.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusCreated, definitionToMap(def, opts, pids))
}

func readOneHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	def, opts, pids, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, definitionToMap(def, opts, pids))
}

func listHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	d := &CustomFieldDefinition{}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	pidStr := c.QueryParam("project_id")
	pid := int64(0)
	if pidStr != "" {
		pid, err = strconv.ParseInt(pidStr, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid project_id")
		}
	}
	defs, err := ReadAll(s, pid)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R7: build a []map (interpreted structs would serialize as {}).
	out := make([]map[string]interface{}, 0, len(defs))
	for i := range defs {
		out = append(out, definitionFieldsMap(&defs[i]))
	}
	return c.JSON(http.StatusOK, out)
}

func updateHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateProjectIDList(req.ProjectIDs); err != nil { // R4: reject sentinel
		return toHTTPError(err)
	}
	d := &CustomFieldDefinition{
		ID: id, Name: req.Name, Type: req.Type, Description: req.Description,
		FieldConfig: req.FieldConfig, DisplayOrder: req.DisplayOrder,
	}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	if _, err := d.Update(s, u, req.Options, req.ProjectIDs); err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R5: re-read for a canonical response (no old-state capture — events deferred).
	rd := &CustomFieldDefinition{ID: id}
	def, opts, pids, err := rd.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, definitionToMap(def, opts, pids))
}

func deleteHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanDelete(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	if err := d.Delete(s); err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
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
			if err := tx.Table("custom_field_value_options").Sync2(&CustomFieldValueOption{}); err != nil {
				return fmt.Errorf("custom-fields: sync value options: %w", err)
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
			// Drop in dependency order: value-options reference values; values + options + assignments reference definitions.
			return tx.DropTables("custom_field_value_options", "custom_field_values", "custom_field_options", "custom_field_projects", "custom_field_definitions")
		},
	}}
}

// RegisterAuthenticatedRoutes mounts the plugin's authenticated routes on the
// /api/v1/plugins/ group. The temporary S8 manager route is removed; IsManager
// is now exercised on the real field-definition endpoints.
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler) // S1 throwaway load-proof
	g.POST("/custom-fields/definitions", createHandler)
	g.GET("/custom-fields/definitions", listHandler)
	g.GET("/custom-fields/definitions/:id", readOneHandler)
	g.PUT("/custom-fields/definitions/:id", updateHandler)
	g.DELETE("/custom-fields/definitions/:id", deleteHandler)
}

func healthHandler(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"name":    "custom-fields",
		"version": "0.1.0",
		"status":  "ok",
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
