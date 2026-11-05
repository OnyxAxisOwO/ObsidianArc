package adapter

import "encoding/json"

func hasCacheControl(parts []Part) bool {
	for _, part := range parts {
		if part.CacheControl != nil {
			return true
		}
	}
	return false
}
func requestHasCacheControl(req ChatRequest) bool {
	if req.CacheControl != nil || hasCacheControl(req.SystemParts) {
		return true
	}
	for _, message := range req.Messages {
		if hasCacheControl(message.Parts) {
			return true
		}
	}
	return false
}

// ValidateControls rejects controls this adapter cannot honour before any request is billed.
func ValidateControls(p Provider, req ChatRequest) error {
	invalid := func(message string) error { return &Error{Kind: ErrorInvalidRequest, Message: message} }
	check := func(cache *CacheControl) error {
		if cache != nil && (cache.Type != "ephemeral" || (cache.TTL != "" && cache.TTL != "5m" && cache.TTL != "1h")) {
			return invalid("cache_control must use ephemeral with a 5m or 1h ttl.")
		}
		return nil
	}
	if err := check(req.CacheControl); err != nil {
		return err
	}
	for _, part := range req.SystemParts {
		if err := check(part.CacheControl); err != nil {
			return err
		}
	}
	for _, message := range req.Messages {
		for _, part := range message.Parts {
			if err := check(part.CacheControl); err != nil {
				return err
			}
		}
	}
	for _, tool := range req.Tools {
		if err := check(tool.CacheControl); err != nil {
			return err
		}
	}
	switch req.ToolChoice.Mode {
	case ToolChoiceRequired:
		if len(req.Tools) == 0 {
			return invalid("tool_choice requires a supported client function tool.")
		}
	case ToolChoiceNamed:
		found := false
		for _, tool := range req.Tools {
			if tool.Name == req.ToolChoice.Name {
				found = true
				break
			}
		}
		if !found {
			return invalid("tool_choice names a function that was not declared as a supported client tool.")
		}
	}
	switch format := req.OutputFormat; format.Type {
	case "", "text":
	case "json_object":
		if p.Kind == KindAnthropic {
			return invalid("This provider requires a JSON schema for structured output.")
		}
	case "json_schema":
		var schema map[string]json.RawMessage
		if json.Unmarshal(format.Schema, &schema) != nil || schema == nil {
			return invalid("Structured output requires a JSON schema object.")
		}
	default:
		return invalid("Unknown structured output format.")
	}
	return nil
}

// Anthropic's input_tokens excludes cache reads and writes. Arc's input count
// includes all three; the cache fields remain subsets for clients to inspect.
type anthropicCacheCreation struct {
	Ephemeral5mInputTokens *int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens *int `json:"ephemeral_1h_input_tokens"`
}
type anthropicTokenUsage struct {
	InputTokens              *int                    `json:"input_tokens"`
	OutputTokens             *int                    `json:"output_tokens"`
	CacheReadInputTokens     *int                    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int                    `json:"cache_creation_input_tokens"`
	CacheCreation            *anthropicCacheCreation `json:"cache_creation"`
}

func tokenValue(value *int) int {
	if value == nil {
		return 0
	}
	return max(0, *value)
}
func (u anthropicTokenUsage) reported() bool {
	return u.InputTokens != nil || u.OutputTokens != nil || u.CacheReadInputTokens != nil || u.CacheCreationInputTokens != nil || u.CacheCreation != nil
}
func (u anthropicTokenUsage) merge(next anthropicTokenUsage) anthropicTokenUsage {
	if next.InputTokens != nil {
		u.InputTokens = next.InputTokens
	}
	if next.OutputTokens != nil {
		u.OutputTokens = next.OutputTokens
	}
	if next.CacheReadInputTokens != nil {
		u.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if next.CacheCreationInputTokens != nil {
		u.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	if next.CacheCreation != nil {
		merged := anthropicCacheCreation{}
		if u.CacheCreation != nil {
			merged = *u.CacheCreation
		}
		if next.CacheCreation.Ephemeral5mInputTokens != nil {
			merged.Ephemeral5mInputTokens = next.CacheCreation.Ephemeral5mInputTokens
		}
		if next.CacheCreation.Ephemeral1hInputTokens != nil {
			merged.Ephemeral1hInputTokens = next.CacheCreation.Ephemeral1hInputTokens
		}
		u.CacheCreation = &merged
	}
	return u
}
func (u anthropicTokenUsage) toUsage() Usage {
	var five, one int
	if u.CacheCreation != nil {
		five = tokenValue(u.CacheCreation.Ephemeral5mInputTokens)
		one = tokenValue(u.CacheCreation.Ephemeral1hInputTokens)
	}
	created := max(tokenValue(u.CacheCreationInputTokens), five+one)
	input := tokenValue(u.InputTokens) + tokenValue(u.CacheReadInputTokens) + created
	return Usage{InputTokens: input, OutputTokens: tokenValue(u.OutputTokens), CacheReadTokens: tokenValue(u.CacheReadInputTokens), CacheCreationTokens: created, CacheCreation5mTokens: five, CacheCreation1hTokens: one, inputReported: input > 0 || (u.InputTokens != nil && u.OutputTokens != nil && *u.InputTokens == 0 && *u.OutputTokens == 0), outputReported: u.OutputTokens != nil}
}
