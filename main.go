package main

import (
	"fmt"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
)

// CustomFieldDefinition is a single custom field's schema. S2 adds columns
// (description, constraints, project assignment, etc.) to this struct.
type CustomFieldDefinition struct {
	ID      int64  `xorm:"bigint autoincr not null unique pk" json:"id"`
	Name    string `xorm:"varchar(255) not null" json:"name"`
	Type    string `xorm:"varchar(50) not null" json:"type"`
	Created string `xorm:"created not null" json:"-"`
	Updated string `xorm:"updated not null" json:"-"`
}

func (CustomFieldDefinition) TableName() string { return "custom_field_definitions" }

// CustomFieldValue is one field's value on one task. S3 refines value typing and adds
// the UNIQUE(field, task) constraint and query indexes.
type CustomFieldValue struct {
	ID                      int64  `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64  `xorm:"bigint not null" json:"custom_field_definition_id"`
	TaskID                  int64  `xorm:"bigint not null" json:"task_id"`
	Value                   string `xorm:"text" json:"value"`
	Created                 string `xorm:"created not null" json:"-"`
	Updated                 string `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }

// CustomFieldsPlugin is the main plugin struct. All capabilities (tables, routes)
// are methods added to this struct in later tasks.
type CustomFieldsPlugin struct{}

func (p *CustomFieldsPlugin) Name() string    { return "custom-fields" }
func (p *CustomFieldsPlugin) Version() string { return "0.1.0" }
func (p *CustomFieldsPlugin) Init() error {
	s := db.NewSession()
	defer s.Close()

	// Auto-increment syntax is the only non-portable part, hence the dialect branch.
	// db.GetDialect() returns "sqlite3" | "mysql" | "postgres" (xorm builder constants).
	switch db.GetDialect() {
	case "sqlite3":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_definitions: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			custom_field_definition_id INTEGER NOT NULL,
			task_id INTEGER NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_values: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	case "mysql":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_definitions: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			custom_field_definition_id BIGINT NOT NULL,
			task_id BIGINT NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_values: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	case "postgres":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_definitions: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id BIGSERIAL PRIMARY KEY,
			custom_field_definition_id BIGINT NOT NULL,
			task_id BIGINT NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_values: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	}

	if err := s.Commit(); err != nil {
		log.Errorf("[custom-fields] failed to commit: %v", err)
		return fmt.Errorf("custom-fields: commit: %w", err)
	}
	log.Infof("[custom-fields] plugin v0.1.0 initialized, tables created")
	return nil
}
func (p *CustomFieldsPlugin) Shutdown() error {
	log.Infof("[custom-fields] plugin shutting down")
	return nil
}

var singleton = &CustomFieldsPlugin{}

func NewPlugin() plugins.Plugin { return singleton }
