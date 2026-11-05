package compat

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

func partsHaveCache(parts []adapter.Part) bool {
	for _, part := range parts {
		if part.CacheControl != nil {
			return true
		}
	}
	return false
}

func readOutputFormat(raw json.RawMessage) (adapter.OutputFormat, error) {
	var wire struct {
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		Schema     json.RawMessage `json:"schema"`
		Strict     *bool           `json:"strict"`
		JSONSchema *struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict"`
		} `json:"json_schema"`
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return adapter.OutputFormat{}, nil
	}
	if json.Unmarshal(raw, &wire) != nil {
		return adapter.OutputFormat{}, badRequest("response_format", "Output format must be an object.")
	}
	if wire.JSONSchema != nil {
		wire.Name = wire.JSONSchema.Name
		wire.Schema = wire.JSONSchema.Schema
		wire.Strict = wire.JSONSchema.Strict
	}
	if wire.Type != "text" && wire.Type != "json_object" && wire.Type != "json_schema" {
		return adapter.OutputFormat{}, badRequest("response_format", "Unknown output format.")
	}
	if wire.Type == "json_schema" {
		var schema map[string]json.RawMessage
		if json.Unmarshal(wire.Schema, &schema) != nil || schema == nil {
			return adapter.OutputFormat{}, badRequest("response_format", "json_schema requires a schema object.")
		}
		if wire.Name == "" {
			wire.Name = "response"
		}
	}
	return adapter.OutputFormat{Type: wire.Type, Name: wire.Name, Schema: wire.Schema, Strict: wire.Strict}, nil
}

func readStop(raw json.RawMessage) ([]string, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var stops []string
	if json.Unmarshal(raw, &stops) != nil || len(stops) > 4 {
		return nil, badRequest("stop", "stop must be a string or an array of at most four strings.")
	}
	return stops, nil
}

func joinResponseCalls(messages []adapter.Message) []adapter.Message {
	out := make([]adapter.Message, 0, len(messages))
	for _, message := range messages {
		callOnly := message.Role == adapter.RoleAssistant && len(message.Parts) > 0
		for _, part := range message.Parts {
			if part.Kind != adapter.PartToolCall {
				callOnly = false
			}
		}
		if last := len(out) - 1; callOnly && last >= 0 && out[last].Role == adapter.RoleAssistant {
			out[last].Parts = append(out[last].Parts, message.Parts...)
		} else {
			out = append(out, message)
		}
	}
	return out
}

func validateProtocolControls(provider adapter.Provider, request adapter.ChatRequest) error {
	err := adapter.ValidateControls(provider, request)
	if failure, ok := err.(*adapter.Error); ok {
		// These diagnostics are authored locally, before any credential is sent.
		return badRequest("", failure.Message)
	}
	return err
}

func toolCacheControl(tool wireTool) *adapter.CacheControl {
	if tool.CacheControl != nil {
		return tool.CacheControl
	}
	return tool.Function.CacheControl
}
func deliveredRefusal(result adapter.Result, failure error) bool {
	var upstream *adapter.Error
	return errors.As(failure, &upstream) && upstream.Kind == adapter.ErrorRefusal && (result.Text != "" || result.Refusal != "")
}
func answerOf(result adapter.Result) string { return strings.TrimSuffix(result.Text, result.Refusal) }
func representableStop(result adapter.Result, failure error) error {
	if failure == nil && strings.EqualFold(strings.TrimSpace(result.FinishReason), "pause_turn") {
		return &adapter.Error{Kind: adapter.ErrorUpstream, Message: "This endpoint cannot continue provider-hosted tool turns."}
	}
	return failure
}

func messagesResultError(result adapter.Result, failure error) error {
	if failure == nil && result.Text == "" && result.Reasoning != "" && len(result.ToolCalls) == 0 {
		return &adapter.Error{Kind: adapter.ErrorUpstream, Message: "The Messages protocol cannot represent an unsigned reasoning-only reply; use Chat Completions or Responses."}
	}
	return failure
}
