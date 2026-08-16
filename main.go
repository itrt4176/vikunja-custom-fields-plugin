package main

import (
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
)

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

var singleton = &CustomFieldsPlugin{}

func NewPlugin() plugins.Plugin { return singleton }
