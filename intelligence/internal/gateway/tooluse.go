// Package gateway — tool-use envelope builder and denormalizer.
//
// Purpose: Convert canonical ToolDefinition slices into provider-specific
//          request shapes (Anthropic tools array or OpenAI functions array),
//          and reverse-convert raw provider tool-call responses back to
//          canonical ToolUseBlock.
// Inputs:  []ToolDefinition, provider name string, raw provider JSON.
// Outputs: []byte (provider-format JSON tools array) or *ToolUseBlock.
// Constraints:
//   - Tool schemas are sanitized before forwarding: only known fields are kept.
//   - No raw model strings — provider identity drives format selection.
//   - All errors wrapped in canonical GatewayError.
//
// SPORT: REGISTRY-FUNCTIONS.md → BuildToolUseEnvelope, DenormalizeToolUseBlock.
package gateway

import (
	"encoding/json"
	"fmt"
)

// ToolDefinition is the canonical representation of an available tool/function.
// Callers supply these; the gateway converts them to the provider format.
type ToolDefinition struct {
	// Name is the tool/function identifier (no spaces, snake_case recommended).
	Name string
	// Description is a human-readable description shown to the model.
	Description string
	// InputSchema is a JSON Schema object describing the tool's parameters.
	// Only "type", "properties", "required", and "description" fields are forwarded;
	// unexpected keys are stripped to avoid provider validation errors.
	InputSchema map[string]any
}

// allowedSchemaKeys is the set of JSON Schema fields forwarded to providers.
// All other keys are stripped (sanitization).
var allowedSchemaKeys = map[string]bool{
	"type":        true,
	"properties":  true,
	"required":    true,
	"description": true,
	"title":       true,
	"enum":        true,
	"items":       true,
	"default":     true,
}

// BuildToolUseEnvelope converts a slice of ToolDefinitions into the provider's
// native JSON representation of the tools/functions array.
//
// For Anthropic: returns a JSON array of {"name","description","input_schema"}.
// For OpenAI-compatible: returns a JSON array of {"type":"function","function":{...}}.
func BuildToolUseEnvelope(tools []ToolDefinition, provider Provider) ([]byte, error) {
	if len(tools) == 0 {
		return []byte("[]"), nil
	}

	name := provider.Name()
	switch {
	case name == "anthropic":
		return buildAnthropicTools(tools)
	default:
		// All OpenAI-compatible providers (openai, gemini, ollama, vllm, etc.)
		return buildOpenAICompatTools(tools)
	}
}

// DenormalizeToolUseBlock converts a raw provider JSON tool-call object into a
// canonical ToolUseBlock. rawJSON should be the provider's tool_use or
// function_call object (not the full message).
//
// For Anthropic: expects {"type":"tool_use","id":"...","name":"...","input":{...}}.
// For OpenAI: expects {"id":"...","function":{"name":"...","arguments":"..."}}.
func DenormalizeToolUseBlock(rawJSON []byte, provider Provider) (*ToolUseBlock, error) {
	if len(rawJSON) == 0 {
		return nil, &GatewayError{
			Provider: provider.Name(),
			Code:     "config",
			Cause:    fmt.Errorf("DenormalizeToolUseBlock: empty rawJSON"),
		}
	}

	name := provider.Name()
	switch {
	case name == "anthropic":
		return denormalizeAnthropic(rawJSON, provider.Name())
	default:
		return denormalizeOpenAICompat(rawJSON, provider.Name())
	}
}

// ---- Anthropic format ----

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func buildAnthropicTools(tools []ToolDefinition) ([]byte, error) {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: sanitizeSchema(t.InputSchema),
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, &GatewayError{Provider: "anthropic", Code: "config",
			Cause: fmt.Errorf("marshal tools: %w", err)}
	}
	return b, nil
}

func denormalizeAnthropic(rawJSON []byte, providerName string) (*ToolUseBlock, error) {
	var obj struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(rawJSON, &obj); err != nil {
		return nil, &GatewayError{Provider: providerName, Code: "upstream",
			Cause: fmt.Errorf("denormalize anthropic tool: %w", err)}
	}
	inputJSON, err := json.Marshal(obj.Input)
	if err != nil {
		return nil, &GatewayError{Provider: providerName, Code: "upstream",
			Cause: fmt.Errorf("re-marshal tool input: %w", err)}
	}
	return &ToolUseBlock{ID: obj.ID, Name: obj.Name, InputJSON: string(inputJSON)}, nil
}

// ---- OpenAI-compatible format ----

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

func buildOpenAICompatTools(tools []ToolDefinition) ([]byte, error) {
	out := make([]openAITool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  sanitizeSchema(t.InputSchema),
			},
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, &GatewayError{Provider: "openai-compat", Code: "config",
			Cause: fmt.Errorf("marshal tools: %w", err)}
	}
	return b, nil
}

func denormalizeOpenAICompat(rawJSON []byte, providerName string) (*ToolUseBlock, error) {
	var obj struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(rawJSON, &obj); err != nil {
		return nil, &GatewayError{Provider: providerName, Code: "upstream",
			Cause: fmt.Errorf("denormalize openai tool: %w", err)}
	}
	return &ToolUseBlock{ID: obj.ID, Name: obj.Function.Name, InputJSON: obj.Function.Arguments}, nil
}

// sanitizeSchema returns a copy of schema with only allowedSchemaKeys kept.
// "properties" values are recursively sanitized. Nil input returns nil.
func sanitizeSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if !allowedSchemaKeys[k] {
			continue // strip unknown fields
		}
		if k == "properties" {
			if props, ok := v.(map[string]any); ok {
				sanitized := make(map[string]any, len(props))
				for propName, propVal := range props {
					if propMap, ok := propVal.(map[string]any); ok {
						sanitized[propName] = sanitizeSchema(propMap)
					} else {
						sanitized[propName] = propVal
					}
				}
				out[k] = sanitized
				continue
			}
		}
		out[k] = v
	}
	return out
}
