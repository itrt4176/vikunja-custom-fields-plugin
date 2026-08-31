package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

const (
	ErrCodeCustomFieldValueInvalid       = 9010
	ErrCodeCustomFieldValueEmpty         = 9011
	ErrCodeCustomFieldOptionNotFound     = 9012
	ErrCodeCustomFieldValueAlreadyExists = 9013
	ErrCodeCustomFieldValueNotFound      = 9014
	ErrCodeCustomFieldTaskNotFound       = 9015
)

type ErrCustomFieldValueInvalid struct{ Type, Detail string }

func (e ErrCustomFieldValueInvalid) Error() string {
	return fmt.Sprintf("invalid value for %s field: %s", e.Type, e.Detail)
}

type ErrCustomFieldValueEmpty struct{}

func (ErrCustomFieldValueEmpty) Error() string { return "value for a required field must not be empty" }

type ErrCustomFieldOptionNotFound struct{ Value string }

func (e ErrCustomFieldOptionNotFound) Error() string {
	return fmt.Sprintf("option value %q is not a valid option for this field", e.Value)
}

type ErrCustomFieldValueAlreadyExists struct{ FieldID, TaskID int64 }

func (e ErrCustomFieldValueAlreadyExists) Error() string {
	return fmt.Sprintf("custom field value already exists for field %d on task %d", e.FieldID, e.TaskID)
}

type ErrCustomFieldValueNotFound struct{ FieldID, TaskID int64 }

func (e ErrCustomFieldValueNotFound) Error() string {
	return fmt.Sprintf("custom field value not found for field %d on task %d", e.FieldID, e.TaskID)
}

type ErrCustomFieldTaskNotFound struct{ ID int64 }

func (e ErrCustomFieldTaskNotFound) Error() string {
	return fmt.Sprintf("task %d not found", e.ID)
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

// validateValue coerces and validates a raw value against a field definition's type
// and constraints. No DB access. For scalar types it returns (storageString, nil, nil);
// for select-types it returns ("", nil, nil) — the option IDs are resolved separately by
// resolveOptionIDs (which needs the options slice the same way this does, but is called
// by the write handler, not here, to keep this pure). raw is the JSON-decoded value.
func validateValue(def *CustomFieldDefinition, options []CustomFieldOption, raw interface{}) (string, []int64, error) {
	switch def.Type {
	case "text", "textarea", "url":
		v, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: def.Type, Detail: "must be a string"}
		}
		if def.FieldConfig.Required && strings.TrimSpace(v) == "" {
			return "", nil, ErrCustomFieldValueEmpty{}
		}
		if def.Type == "url" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme == "" {
				return "", nil, ErrCustomFieldValueInvalid{Type: "url", Detail: "must be a valid URL with a scheme"}
			}
		}
		return v, nil, nil
	case "integer":
		switch n := raw.(type) {
		case float64: // JSON numbers arrive as float64
			i := int64(n)
			if float64(i) != n {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "out of range"}
			}
			if def.FieldConfig.Min != nil && float64(i) < *def.FieldConfig.Min {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "below min"}
			}
			if def.FieldConfig.Max != nil && float64(i) > *def.FieldConfig.Max {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "above max"}
			}
			return strconv.FormatInt(i, 10), nil, nil
		case string:
			i, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "not a valid integer"}
			}
			if def.FieldConfig.Min != nil && float64(i) < *def.FieldConfig.Min {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "below min"}
			}
			if def.FieldConfig.Max != nil && float64(i) > *def.FieldConfig.Max {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "above max"}
			}
			return strconv.FormatInt(i, 10), nil, nil
		}
		return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "must be a number"}
	case "decimal":
		var f float64
		switch n := raw.(type) {
		case float64:
			f = n
		case string:
			v, err := strconv.ParseFloat(n, 64)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "not a valid number"}
			}
			f = v
		default:
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "must be a number"}
		}
		if def.FieldConfig.Min != nil && f < *def.FieldConfig.Min {
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "below min"}
		}
		if def.FieldConfig.Max != nil && f > *def.FieldConfig.Max {
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "above max"}
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil, nil
	case "date":
		s, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "date", Detail: "must be an ISO date string"}
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return "", nil, ErrCustomFieldValueInvalid{Type: "date", Detail: "must be YYYY-MM-DD"}
		}
		return s, nil, nil
	case "datetime":
		s, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "datetime", Detail: "must be an RFC3339 string"}
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return "", nil, ErrCustomFieldValueInvalid{Type: "datetime", Detail: "must be RFC3339"}
		}
		return s, nil, nil
	case "checkbox":
		switch v := raw.(type) {
		case bool:
			return strconv.FormatBool(v), nil, nil
		case string:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "checkbox", Detail: "must be a boolean"}
			}
			return strconv.FormatBool(b), nil, nil
		}
		return "", nil, ErrCustomFieldValueInvalid{Type: "checkbox", Detail: "must be a boolean"}
	case "select", "multiselect":
		// validate the option value string(s) are in the field's current options' values.
		// returns no storage string — the handler calls resolveOptionIDs to get the IDs.
		validValues := map[string]struct{}{}
		for _, o := range options {
			validValues[o.Value] = struct{}{}
		}
		if def.Type == "select" {
			s, ok := raw.(string)
			if !ok {
				return "", nil, ErrCustomFieldValueInvalid{Type: "select", Detail: "must be a string option value"}
			}
			if def.FieldConfig.Required && s == "" {
				return "", nil, ErrCustomFieldValueEmpty{}
			}
			if s != "" {
				if _, ok := validValues[s]; !ok {
					return "", nil, ErrCustomFieldOptionNotFound{Value: s}
				}
			}
			return s, nil, nil
		}
		// multiselect: raw is a []interface{} of strings (JSON array)
		arr, ok := raw.([]interface{})
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "must be an array of option values"}
		}
		vals := make([]string, 0, len(arr))
		for _, e := range arr {
			s, ok := e.(string)
			if !ok {
				return "", nil, ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "array elements must be strings"}
			}
			if _, ok := validValues[s]; !ok {
				return "", nil, ErrCustomFieldOptionNotFound{Value: s}
			}
			vals = append(vals, s)
		}
		if def.FieldConfig.Required && len(vals) == 0 {
			return "", nil, ErrCustomFieldValueEmpty{}
		}
		// join for a notional storage string (not actually stored for select-types, but
		// return it for completeness; the handler uses resolveOptionIDs for the child rows)
		return strings.Join(vals, "\x00"), nil, nil
	}
	return "", nil, ErrCustomFieldInvalidType{Type: def.Type}
}

// resolveOptionIDs maps option value strings to option IDs by matching the passed
// options slice's Value field. Called by write handlers after validateValue succeeds.
func resolveOptionIDs(options []CustomFieldOption, valueStrings []string) ([]int64, error) {
	byValue := map[string]int64{}
	for _, o := range options {
		byValue[o.Value] = o.ID
	}
	ids := make([]int64, 0, len(valueStrings))
	for _, v := range valueStrings {
		id, ok := byValue[v]
		if !ok {
			return nil, ErrCustomFieldOptionNotFound{Value: v}
		}
		ids = append(ids, id)
	}
	return ids, nil
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

// Value access is gated on task-level permission, not the management whitelist:
// a value is visible/writable exactly when its task is. These delegate to the
// host's models.Task.CanRead/CanUpdate with the same *user.User (yaegi accepts
// it in place of web.Auth). canWrite is the shared write gate (create/update/
// delete all require task write access).

func (v *CustomFieldValue) CanRead(s *xorm.Session, u *user.User) (bool, error) {
	t := &models.Task{ID: v.TaskID}
	ok, _, err := t.CanRead(s, u) // discard maxPermission (3-return → 2-return)
	if err != nil {
		// a not-found task means no access; surface as false, not 500
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return ok, nil
}

func (v *CustomFieldValue) canWrite(s *xorm.Session, u *user.User) (bool, error) {
	t := &models.Task{ID: v.TaskID}
	ok, err := t.CanUpdate(s, u)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return ok, nil
}

func (v *CustomFieldValue) CanCreate(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }
func (v *CustomFieldValue) CanUpdate(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }
func (v *CustomFieldValue) CanDelete(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }

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

// valueItem is one entry of the bulk write body. R4: the bulk POST
// /tasks/:task/custom-fields body is a BARE JSON array of these — not a
// {"values": [...]} wrapper — so it is decoded directly with encoding/json.
type valueItem struct {
	CustomFieldDefinitionID int64       `json:"custom_field_definition_id"`
	Value                   interface{} `json:"value"`
}

// singleValueRequest is the body of the per-field POST/PUT: {"value": ...}.
type singleValueRequest struct {
	Value interface{} `json:"value"`
}

// valueToMap builds the {value, field} entry for the read response. fieldMap is
// the definition's metadata (built by S2's definitionToMap, reused). value is
// the coerced native value, or nil if absent/invalid.
func valueToMap(value interface{}, fieldMap map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"value": value,
		"field": fieldMap,
	}
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
	// S3 value errors. R1: one unique prefix per case — yaegi evaluates only the
	// first expression of a multi-expression case clause, so HasPrefix+Contains
	// pairs would collide. Prefixes here are the Task 2 error messages; all are
	// distinct from S2's "custom field definition ..." / "custom field name ...".
	case strings.HasPrefix(msg, "invalid value for"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "value for a required field must not be empty"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "option value"):
		return echo.NewHTTPError(http.StatusBadRequest, msg)
	case strings.HasPrefix(msg, "custom field value already exists"):
		return echo.NewHTTPError(http.StatusConflict, msg)
	case strings.HasPrefix(msg, "custom field value not found"):
		return echo.NewHTTPError(http.StatusNotFound, msg)
	case strings.HasPrefix(msg, "task ") && strings.Contains(msg, "not found"):
		return echo.NewHTTPError(http.StatusNotFound, msg)
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

// ── Value handlers (S3). Read: collection + per-field GET, shaped
// {definition_id: {value, field}}. Write: bulk upsert (bare-array body, R4),
// per-field create/update/delete sharing the writeValue helper.

// fieldAppliesToProject mirrors S2's ReadAll project filter (NotificationProjectFilter
// logic, not builder syntax): a field applies to a project if it has a
// custom_field_projects row for that project OR the global sentinel
// (project_id = 0). The OR is parenthesized inside a single Where — xorm's
// Where/And chaining would emit the OR un-parenthesized, and SQL's precedence
// would then detach the definition filter from the global branch.
func fieldAppliesToProject(s *xorm.Session, defID, projectID int64) (bool, error) {
	return s.Table("custom_field_projects").
		Where("custom_field_definition_id = ? AND (project_id = ? OR project_id = 0)", defID, projectID).
		Exist(&CustomFieldProject{})
}

// coerceReadValue returns the native JSON value for a stored string + field
// type, or nil if the value can't be coerced (invalid → absent per the
// read-path policy: a value that no longer parses reads as null, never 500).
// Select-type values never reach this — readValuesForTask resolves them from
// custom_field_value_options (the API exposes the option value string, not the
// stored option id).
func coerceReadValue(def *CustomFieldDefinition, stored string) interface{} {
	switch def.Type {
	case "integer":
		i, err := strconv.ParseInt(stored, 10, 64)
		if err != nil {
			return nil
		}
		return i
	case "decimal":
		f, err := strconv.ParseFloat(stored, 64)
		if err != nil {
			return nil
		}
		return f
	case "checkbox":
		b, err := strconv.ParseBool(stored)
		if err != nil {
			return nil
		}
		return b
	default:
		if stored == "" {
			return nil
		}
		return stored
	}
}

// readValuesForTask fetches the task's values, filters by project assignment
// (AC#4), coerces to native types, and returns the {definition_id: {value,
// field}} map. Shared by the collection GET, the per-field GET, and the write
// handlers' canonical re-read responses.
func readValuesForTask(s *xorm.Session, taskID int64) (map[string]interface{}, error) {
	t, err := models.GetTaskByIDSimple(s, taskID)
	if err != nil {
		return nil, ErrCustomFieldTaskNotFound{ID: taskID}
	}
	var values []CustomFieldValue
	if err := s.Table("custom_field_values").Where("task_id = ?", taskID).Find(&values); err != nil {
		return nil, fmt.Errorf("custom-fields: get values: %w", err)
	}
	out := map[string]interface{}{}
	for _, v := range values {
		// AC#4: the field must be assigned to the task's project.
		applies, err := fieldAppliesToProject(s, v.CustomFieldDefinitionID, t.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("custom-fields: check field assignment: %w", err)
		}
		if !applies {
			continue
		}
		// fetch the definition + options (field metadata + type coercion)
		d := &CustomFieldDefinition{ID: v.CustomFieldDefinitionID}
		def, opts, pids, err := d.ReadOne(s)
		if err != nil {
			// definition deleted while its value row remains (S2 delete doesn't
			// cascade to values yet — Task 7 fixes that): skip the orphan. Other
			// failures are real and abort. yaegi can't type-assert interpreted
			// errors (see toHTTPError), so discriminate by message prefix.
			if strings.HasPrefix(err.Error(), "custom field definition ") {
				continue
			}
			return nil, err
		}
		fieldMap := definitionToMap(def, opts, pids)
		var native interface{}
		if isSelectLike(def.Type) {
			// resolve the option value strings from the child table — the value
			// row itself is empty for select-types; the option ids live here
			var childRows []CustomFieldValueOption
			if err := s.Table("custom_field_value_options").Where("custom_field_value_id = ?", v.ID).Find(&childRows); err != nil {
				return nil, fmt.Errorf("custom-fields: get value options: %w", err)
			}
			if len(childRows) == 0 {
				native = nil
			} else {
				optIDs := make([]int64, 0, len(childRows))
				for _, c := range childRows {
					optIDs = append(optIDs, c.CustomFieldOptionID)
				}
				valStrings := make([]string, 0, len(childRows))
				for _, o := range opts {
					for _, id := range optIDs {
						if o.ID == id {
							valStrings = append(valStrings, o.Value)
						}
					}
				}
				if def.Type == "select" {
					if len(valStrings) > 0 {
						native = valStrings[0]
					} else {
						native = nil
					}
				} else {
					native = valStrings
				}
			}
		} else {
			native = coerceReadValue(def, v.Value)
		}
		out[strconv.FormatInt(v.CustomFieldDefinitionID, 10)] = valueToMap(native, fieldMap)
	}
	return out, nil
}

func listValuesHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID}
	ok, err := v.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no access to this task")
	}
	out, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, out)
}

func readOneValueHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	fieldID, err := strconv.ParseInt(c.Param("field_id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid field id")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no access to this task")
	}
	// fetch this one value through the shared read path (same AC#4 filter +
	// coercion), then pick the requested key — absent → 404
	all, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	entry, present := all[strconv.FormatInt(fieldID, 10)]
	if !present {
		return echo.NewHTTPError(http.StatusNotFound, "value not found")
	}
	return c.JSON(http.StatusOK, entry)
}

// writeValue is the shared validate-then-write block for the three write
// handlers (bulk upsert, per-field create, per-field update). It resolves the
// task's project, enforces the AC#4 project-assignment check, validates the raw
// value against the definition, replaces any existing value row (with its
// select-type child rows), and inserts the new row (plus child rows for
// select-types). It commits nothing — the caller owns the session and its
// commit, so a multi-item batch stays atomic.
//
// Errors come back pre-translated (toHTTPError / echo.NewHTTPError) so handlers
// return them as-is; only s.Commit() and the canonical re-read remain
// handler-side.
func writeValue(s *xorm.Session, taskID, fieldID int64, def *CustomFieldDefinition, opts []CustomFieldOption, raw interface{}) (*CustomFieldValue, error) {
	// AC#4: the field must be assigned to the task's project to be writable.
	t, err := models.GetTaskByIDSimple(s, taskID)
	if err != nil {
		return nil, toHTTPError(ErrCustomFieldTaskNotFound{ID: taskID})
	}
	applies, err := fieldAppliesToProject(s, fieldID, t.ProjectID)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Errorf("custom-fields: check field assignment: %w", err).Error())
	}
	if !applies {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "field is not assigned to this task's project")
	}
	storage, _, err := validateValue(def, opts, raw)
	if err != nil {
		return nil, toHTTPError(err)
	}
	// validateValue's second return is nil for select-types (its storage-string
	// position is notional there), so re-extract the option value strings from
	// raw for resolveOptionIDs.
	var valStrings []string
	if isSelectLike(def.Type) {
		if def.Type == "select" {
			if vs, ok := raw.(string); ok && vs != "" {
				valStrings = []string{vs}
			}
		} else {
			arr, ok := raw.([]interface{})
			if !ok {
				return nil, toHTTPError(ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "must be an array of option values"})
			}
			valStrings = make([]string, 0, len(arr))
			for _, e := range arr {
				vs, ok := e.(string)
				if !ok {
					return nil, toHTTPError(ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "array elements must be strings"})
				}
				valStrings = append(valStrings, vs)
			}
		}
	}
	// upsert: replace any existing value for (field, task) — child rows first,
	// they reference the value row's id.
	if _, err := s.Table("custom_field_value_options").
		Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE custom_field_definition_id = ? AND task_id = ?)", fieldID, taskID).
		Delete(&CustomFieldValueOption{}); err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Errorf("custom-fields: clear value options: %w", err).Error())
	}
	if _, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Delete(&CustomFieldValue{}); err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Errorf("custom-fields: clear value: %w", err).Error())
	}
	val := &CustomFieldValue{CustomFieldDefinitionID: fieldID, TaskID: taskID}
	if isSelectLike(def.Type) {
		val.Value = "" // the option ids live in custom_field_value_options
	} else {
		val.Value = storage
	}
	if _, err := s.Table("custom_field_values").Insert(val); err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Errorf("custom-fields: insert value: %w", err).Error())
	}
	if isSelectLike(def.Type) && len(valStrings) > 0 {
		optIDs, err := resolveOptionIDs(opts, valStrings)
		if err != nil {
			return nil, toHTTPError(err)
		}
		childRows := make([]CustomFieldValueOption, len(optIDs))
		for i, id := range optIDs {
			childRows[i] = CustomFieldValueOption{CustomFieldValueID: val.ID, CustomFieldOptionID: id}
		}
		if _, err := s.Table("custom_field_value_options").Insert(&childRows); err != nil {
			return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Errorf("custom-fields: insert value options: %w", err).Error())
		}
	}
	return val, nil
}

// bulkUpsertHandler (POST /tasks/:task/custom-fields) writes one or more field
// values in a single request. R4: the body is a BARE JSON array of valueItems,
// not a wrapper object, so it is decoded directly with encoding/json — echo v5's
// Bind is not robust for a bare array.
func bulkUpsertHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	var items []valueItem
	if err := json.NewDecoder(c.Request().Body).Decode(&items); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID}
	ok, err := v.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	for _, item := range items {
		d := &CustomFieldDefinition{ID: item.CustomFieldDefinitionID}
		def, opts, _, err := d.ReadOne(s)
		if err != nil {
			return toHTTPError(err)
		}
		if _, err := writeValue(s, taskID, item.CustomFieldDefinitionID, def, opts, item.Value); err != nil {
			return err
		}
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// re-read for the canonical response
	out, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// createOneValueHandler (POST /tasks/:task/custom-fields/:field_id) creates one
// field value. Create-only: 409 if a value already exists for (field, task).
func createOneValueHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	fieldID, err := strconv.ParseInt(c.Param("field_id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid field id")
	}
	var req singleValueRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanCreate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	// create-only: 409 if a value already exists for (field, task)
	exists, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Exist(&CustomFieldValue{})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if exists {
		return toHTTPError(ErrCustomFieldValueAlreadyExists{FieldID: fieldID, TaskID: taskID})
	}
	d := &CustomFieldDefinition{ID: fieldID}
	def, opts, _, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	if _, err := writeValue(s, taskID, fieldID, def, opts, req.Value); err != nil {
		return err
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	all, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusCreated, all[strconv.FormatInt(fieldID, 10)])
}

// updateOneValueHandler (PUT /tasks/:task/custom-fields/:field_id) replaces one
// field value. Replace-only: 404 if no value exists for (field, task).
func updateOneValueHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	fieldID, err := strconv.ParseInt(c.Param("field_id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid field id")
	}
	var req singleValueRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	// replace-only: 404 if no value exists for (field, task)
	exists, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Exist(&CustomFieldValue{})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !exists {
		return toHTTPError(ErrCustomFieldValueNotFound{FieldID: fieldID, TaskID: taskID})
	}
	d := &CustomFieldDefinition{ID: fieldID}
	def, opts, _, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	if _, err := writeValue(s, taskID, fieldID, def, opts, req.Value); err != nil {
		return err
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	all, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, all[strconv.FormatInt(fieldID, 10)])
}

// deleteOneValueHandler (DELETE /tasks/:task/custom-fields/:field_id) removes a
// field value and its select-type child rows.
func deleteOneValueHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.Param("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	fieldID, err := strconv.ParseInt(c.Param("field_id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid field id")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanDelete(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	if _, err := s.Table("custom_field_value_options").
		Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE custom_field_definition_id = ? AND task_id = ?)", fieldID, taskID).
		Delete(&CustomFieldValueOption{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if _, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Delete(&CustomFieldValue{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
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
	// S3 field values. The group mounts at /api/v1/plugins and every plugin path
	// carries the /custom-fields namespace itself (S2 convention), so the value
	// resource is /api/v1/plugins/custom-fields/tasks/:task/custom-fields[/:field_id]
	// — the native /api/v2/tasks/{task}/custom-fields shape under the plugin prefix.
	g.GET("/custom-fields/tasks/:task/custom-fields", listValuesHandler)
	g.POST("/custom-fields/tasks/:task/custom-fields", bulkUpsertHandler)
	g.GET("/custom-fields/tasks/:task/custom-fields/:field_id", readOneValueHandler)
	g.POST("/custom-fields/tasks/:task/custom-fields/:field_id", createOneValueHandler)
	g.PUT("/custom-fields/tasks/:task/custom-fields/:field_id", updateOneValueHandler)
	g.DELETE("/custom-fields/tasks/:task/custom-fields/:field_id", deleteOneValueHandler)
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
