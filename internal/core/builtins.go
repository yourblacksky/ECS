package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"ecs/internal/iot"
	"ecs/internal/skills"
	"ecs/internal/toolbox"
	"ecs/internal/tools"
)

type capabilityCatalogItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type builtinRegistration struct {
	handler      tools.BuiltinHandler
	availability tools.BuiltinAvailability
}

func registerBuiltinTools(
	toolRegistry *tools.Registry,
	skillRegistry *skills.Registry,
	extra ...map[string]builtinRegistration,
) error {
	for _, manifest := range toolRegistry.All() {
		if manifest.Command != "@builtin" {
			continue
		}
		registration, configured := extraBuiltinRegistration(manifest.Name, extra)
		handler := registration.handler
		if !configured {
			handler = builtinToolHandler(manifest.Name, toolRegistry, skillRegistry)
		}
		if handler == nil {
			return fmt.Errorf("tool %q declares @builtin but has no registered implementation", manifest.Name)
		}
		if err := toolRegistry.RegisterBuiltinWithAvailability(
			manifest.Name, handler, registration.availability,
		); err != nil {
			return err
		}
	}
	return nil
}

func extraBuiltinRegistration(
	name string,
	registrations []map[string]builtinRegistration,
) (builtinRegistration, bool) {
	for _, entries := range registrations {
		if registration, ok := entries[name]; ok {
			return registration, true
		}
	}
	return builtinRegistration{}, false
}

func iotBuiltinRegistrations(connector *iot.Connector) map[string]builtinRegistration {
	return map[string]builtinRegistration{
		"iot_http_request": {
			handler: connector.HTTP, availability: connector.HTTPAvailability,
		},
		"iot_mqtt": {
			handler: connector.MQTT, availability: connector.MQTTAvailability,
		},
	}
}

func builtinToolHandler(
	name string,
	toolRegistry *tools.Registry,
	skillRegistry *skills.Registry,
) tools.BuiltinHandler {
	if name == "capabilities" {
		return func(_ context.Context, _ json.RawMessage) (string, error) {
			toolManifests := toolRegistry.All()
			skillManifests := skillRegistry.All()
			toolItems := make([]capabilityCatalogItem, 0, len(toolManifests))
			for _, manifest := range toolManifests {
				toolItems = append(toolItems, capabilityCatalogItem{
					Name: manifest.Name, Description: manifest.Description,
				})
			}
			skillItems := make([]capabilityCatalogItem, 0, len(skillManifests))
			for _, manifest := range skillManifests {
				skillItems = append(skillItems, capabilityCatalogItem{
					Name: manifest.Name, Description: manifest.Description,
				})
			}
			encoded, err := json.Marshal(map[string]any{
				"count":       len(toolItems),
				"tool_count":  len(toolItems),
				"skill_count": len(skillItems),
				"tools":       toolItems,
				"skills":      skillItems,
				"summary":     capabilitySummary(len(toolItems), len(skillItems)),
			})
			return string(encoded), err
		}
	}
	return toolbox.Handler(name)
}

func capabilitySummary(toolCount, skillCount int) string {
	return "当前节点有 " + strconv.Itoa(toolCount) +
		" 个 Tool、" + strconv.Itoa(skillCount) + " 个 Skill"
}
