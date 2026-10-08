package acp

import (
	"encoding/json"
)

// NormalizeSessionUpdate selects the plane transcript subset of ACP updates.
// Provider output, binary content, locations and opaque metadata are omitted.
func NormalizeSessionUpdate(update map[string]any) map[string]any {
	kind, _ := update["sessionUpdate"].(string)
	out := map[string]any{"sessionUpdate": kind}
	switch kind {
	case "agent_message_chunk", "user_message_chunk":
		content, _ := update["content"].(map[string]any)
		text, ok := content["text"].(string)
		if content["type"] != "text" || !ok {
			return nil
		}
		out["content"] = map[string]any{"type": "text", "text": text}
	case "tool_call", "tool_call_update":
		for _, field := range []string{"toolCallId", "status"} {
			if value, ok := update[field].(string); ok {
				out[field] = value
			}
		}
		if kind == "tool_call_update" {
			return out
		}
		for _, field := range []string{"title", "kind"} {
			if value, ok := update[field].(string); ok {
				out[field] = value
			}
		}
		input := map[string]any{}
		if values, ok := update["rawInput"].(map[string]any); ok {
			// Match the existing Claude summary: keep intent and location,
			// omit file bodies, replacement text, prompts and opaque arguments.
			for _, key := range []string{"command", "description", "file_path", "notebook_path", "path", "pattern", "glob", "url", "query", "subagent_type", "skill", "title"} {
				if _, ok := values[key]; !ok {
					continue
				}
				value := values[key]
				if text, ok := value.(string); ok {
					input[key] = text
				} else {
					raw, err := json.Marshal(value)
					if err == nil {
						input[key] = string(raw)
					}
				}
			}
		}
		out["rawInput"] = input
	default:
		return nil
	}
	return out
}
