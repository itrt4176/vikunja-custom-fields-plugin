package main

import (
	"fmt"
	"net/http"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"

	"github.com/labstack/echo/v5"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

// CustomFieldDefinition is a single custom field's schema. S2 adds columns
// (description, constraints, project assignment, etc.) to this struct.
type CustomFieldDefinition struct {
	ID      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	Name    string    `xorm:"varchar(255) not null" json:"name"`
	Type    string    `xorm:"varchar(50) not null" json:"type"`
	Created time.Time `xorm:"created not null" json:"-"`
	Updated time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldDefinition) TableName() string { return "custom_field_definitions" }

// CustomFieldValue is one field's value on one task. S3 refines value typing and adds
// the UNIQUE(field, task) constraint and query indexes.
type CustomFieldValue struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null" json:"custom_field_definition_id"`
	TaskID                  int64     `xorm:"bigint not null" json:"task_id"`
	Value                   string    `xorm:"text" json:"value"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }

// CustomFieldsPlugin is the main plugin struct. All capabilities (tables, routes)
// are methods added to this struct in later tasks.
type CustomFieldsPlugin struct{}

func (p *CustomFieldsPlugin) Name() string    { return "custom-fields" }
func (p *CustomFieldsPlugin) Version() string { return "0.1.0" }

func (p *CustomFieldsPlugin) Init() error {
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
func (p *CustomFieldsPlugin) Migrations() []*xormigrate.Migration {
	return []*xormigrate.Migration{{
		ID:          "20260829160000-create-custom-field-tables",
		Description: "Create custom_field_definitions and custom_field_values",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Table("custom_field_definitions").Sync2(&CustomFieldDefinition{}); err != nil {
				return fmt.Errorf("custom-fields: sync definitions: %w", err)
			}
			if err := tx.Table("custom_field_values").Sync2(&CustomFieldValue{}); err != nil {
				return fmt.Errorf("custom-fields: sync values: %w", err)
			}
			return nil
		},
		Rollback: func(tx *xorm.Engine) error {
			// Drop in dependency order: values reference definitions.
			return tx.DropTables("custom_field_values", "custom_field_definitions")
		},
	}}
}

// RegisterAuthenticatedRoutes mounts the plugin's authenticated routes on the
// /api/v1/plugins/ group.
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)
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