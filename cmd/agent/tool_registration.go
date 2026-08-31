package main

import (
	"fmt"

	appcore "paw/internal/app"
	"paw/internal/todo"
	"paw/internal/tool"
	selecttool "paw/internal/tool/select"
)

func registerMainAgentTools(registry *tool.Registry, broker *todo.Broker) error {
	tools := appcore.NewToolset(broker)
	return tools.RegisterMain(registry)
}

func registerInteractiveTools(registry *tool.Registry, broker *selecttool.Broker) error {
	tools := appcore.NewToolset(nil)
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if broker == nil {
		return fmt.Errorf("selection broker is nil")
	}
	return tools.RegisterInteractive(registry, broker)
}
