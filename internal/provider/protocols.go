package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func runGrok(rw io.ReadWriteCloser, turn Turn, decide Decide) (Result, error) {
	r := bufio.NewReader(rw)
	if err := writeJSON(rw, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": GrokACPVersion},
	}); err != nil {
		return Result{}, err
	}
	var init struct {
		Result struct {
			ProtocolVersion int `json:"protocolVersion"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := readJSON(r, &init); err != nil {
		return Result{}, err
	}
	if init.Error != nil || init.Result.ProtocolVersion != GrokACPVersion {
		return Result{}, fmt.Errorf("provider: grok protocol %d refused", init.Result.ProtocolVersion)
	}
	method := "session/new"
	params := map[string]any{"cwd": turn.Workspace}
	if turn.Cursor != "" {
		method = "session/load"
		params = map[string]any{"sessionId": turn.Cursor, "cwd": turn.Workspace}
	}
	if err := writeJSON(rw, map[string]any{"jsonrpc": "2.0", "id": 2, "method": method, "params": params}); err != nil {
		return Result{}, err
	}
	var opened struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	if err := readJSON(r, &opened); err != nil {
		return Result{}, err
	}
	if err := writeJSON(rw, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{"sessionId": opened.Result.SessionID, "prompt": turn.Prompt},
	}); err != nil {
		return Result{}, err
	}
	return readProviderLoop(r, rw, opened.Result.SessionID, decide, "grok")
}

// runClaude drives one Claude Code stream-json turn. Claude Code emits
// system/init only after the first user message, so the prompt goes first;
// init is still validated before any other event is acted on.
func runClaude(rw io.ReadWriteCloser, turn Turn, decide Decide) (Result, error) {
	r := bufio.NewReader(rw)
	if err := writeJSON(rw, map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": turn.Prompt},
	}); err != nil {
		return Result{}, err
	}
	var init struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Version string `json:"claude_code_version"`
		Session string `json:"session_id"`
	}
	if err := readJSON(r, &init); err != nil {
		return Result{}, err
	}
	// The argv fixes the stream format; 2.1.283's init names no protocol,
	// so the pinned version is the check.
	if init.Type != "system" || init.Subtype != "init" || init.Version != ClaudeCodeVersion {
		return Result{}, fmt.Errorf("provider: claude protocol refused")
	}
	if turn.Cursor != "" && init.Session != turn.Cursor {
		return Result{}, fmt.Errorf("provider: claude resume cursor mismatch")
	}
	res, err := readProviderLoop(r, rw, init.Session, decide, "claude")
	if err != nil {
		return Result{}, err
	}
	if res.Cursor == "" {
		res.Cursor = init.Session
	}
	return res, nil
}

func runCodex(rw io.ReadWriteCloser, inst Instance, turn Turn, decide Decide) (Result, error) {
	r := bufio.NewReader(rw)
	if err := writeJSON(rw, map[string]any{
		"id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": CodexAppServerProto, "instance": inst.ID},
	}); err != nil {
		return Result{}, err
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := readJSON(r, &init); err != nil {
		return Result{}, err
	}
	if init.Result.ProtocolVersion != CodexAppServerProto {
		return Result{}, fmt.Errorf("provider: codex protocol %q refused", init.Result.ProtocolVersion)
	}
	method := "thread/start"
	params := map[string]any{}
	if turn.Cursor != "" {
		method = "thread/resume"
		params["threadId"] = turn.Cursor
	}
	if err := writeJSON(rw, map[string]any{"id": 2, "method": method, "params": params}); err != nil {
		return Result{}, err
	}
	var opened struct {
		Result struct {
			ThreadID string `json:"threadId"`
		} `json:"result"`
	}
	if err := readJSON(r, &opened); err != nil {
		return Result{}, err
	}
	if err := writeJSON(rw, map[string]any{
		"id": 3, "method": "turn/start",
		"params": map[string]any{"threadId": opened.Result.ThreadID, "prompt": turn.Prompt},
	}); err != nil {
		return Result{}, err
	}
	return readProviderLoop(r, rw, opened.Result.ThreadID, decide, "codex")
}

func readProviderLoop(r *bufio.Reader, w io.Writer, cursor string, decide Decide, kind string) (Result, error) {
	var res Result
	res.Cursor = cursor
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return res, err
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			return res, err
		}
		if kind == "claude" && msg["type"] == "control_request" {
			ev, err := answerClaudeControl(w, line, decide)
			if err != nil {
				return res, err
			}
			res.Events = append(res.Events, ev)
			continue
		}
		if msg["method"] == "session/request_permission" || msg["method"] == "item/permission" {
			raw, _ := json.Marshal(msg["options"])
			if params, ok := msg["params"].(map[string]any); ok {
				raw, _ = json.Marshal(params["options"])
			}
			var options []Option
			_ = json.Unmarshal(raw, &options)
			optionID, allow := rejectMissing(options, decide, line)
			replyID := fmt.Sprint(msg["id"])
			if !allow {
				optionID = "deny"
			}
			if err := writePermission(w, kind, replyID, optionID, allow); err != nil {
				return res, err
			}
			res.Events = append(res.Events, Event{Kind: "permission", Body: optionID, OptionID: optionID})
			continue
		}
		if msg["type"] == "rate_limit_event" || msg["method"] == "item/usageLimit" {
			reset, _ := msg["resets_at"].(string)
			ev := Event{Kind: "usage_limit", Reset: reset, ResetMissing: reset == ""}
			res.Events = append(res.Events, ev)
			continue
		}
		if msg["method"] == "item/question" {
			params, _ := msg["params"].(map[string]any)
			id, _ := params["id"].(string)
			body, _ := params["body"].(string)
			res.Events = append(res.Events, Event{Kind: "question", Body: body, OptionID: id})
			continue
		}
		if msg["type"] == "result" || msg["method"] == "turn/completed" || msg["method"] == "session/prompt" && msg["result"] != nil {
			return res, nil
		}
		if _, ok := msg["result"]; ok && msg["method"] == nil && msg["id"] != nil && kind != "claude" {
			// prompt response
			if kind == "grok" || kind == "codex" {
				return res, nil
			}
		}
		if kind == "claude" {
			continue
		}
		text := string(line)
		kindName := "transcript"
		if strings.Contains(text, "tool_call") || strings.Contains(text, "tool_use") || msg["method"] == "item/tool" {
			kindName = "tool_call"
		}
		res.Events = append(res.Events, Event{Kind: kindName, Body: strings.TrimSpace(text)})
		if msg["type"] == "result" {
			return res, nil
		}
	}
}

func writePermission(w io.Writer, kind, id, optionID string, allow bool) error {
	if !allow {
		optionID = "deny"
	}
	switch kind {
	case "codex":
		return writeJSON(w, map[string]any{"id": id, "result": map[string]any{"optionId": optionID}})
	default:
		idRaw, err := json.Marshal(id)
		if err != nil {
			return err
		}
		return writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(idRaw), "result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}})
	}
}

// claudeToolKinds maps Claude Code tool names to ACP tool kinds by their
// effect, so policy rules and the fence judge Claude like any other guest.
// "think" tools only plan or delegate: a subagent's own tool calls come
// back through can_use_tool. MCP tools and tools that act beyond the turn
// (messages, schedules, notifications) stay "other" and need a tool rule
// or an operator approval.
var claudeToolKinds = map[string]string{
	"Read": "read", "NotebookRead": "read", "Skill": "read", "LSP": "read",
	"Write": "edit", "Edit": "edit", "MultiEdit": "edit", "NotebookEdit": "edit",
	"Glob": "search", "Grep": "search", "LS": "search", "ToolSearch": "search",
	"Bash": "execute", "BashOutput": "execute", "KillShell": "execute",
	"TaskOutput": "execute", "TaskStop": "execute", "Monitor": "execute",
	"WebFetch": "fetch", "WebSearch": "fetch",
	"Agent": "think", "Task": "think", "TodoWrite": "think",
	"EnterPlanMode": "switch_mode", "ExitPlanMode": "switch_mode",
}

// answerClaudeControl answers one Claude Code control request. Only
// can_use_tool is supported; any other subtype is refused. The tool call
// is translated to the ACP shape the gate reads, and a denial or a missing
// decision replies deny.
func answerClaudeControl(w io.Writer, line []byte, decide Decide) (Event, error) {
	var req struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype  string         `json:"subtype"`
			ToolName string         `json:"tool_name"`
			Input    map[string]any `json:"input"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return Event{}, err
	}
	allow := false
	decision := map[string]any{"behavior": "deny", "message": "denied by rusui policy"}
	if req.Request.ToolName == "AskUserQuestion" {
		// No human answers inside a headless turn; waiting for approval
		// would only hold the turn until its deadline.
		decision["message"] = "no one can answer questions during this turn; decide yourself or report the uncertainty in your result"
	} else if req.Request.Subtype == "can_use_tool" {
		kind, ok := claudeToolKinds[req.Request.ToolName]
		if !ok {
			kind = "other"
		}
		call := map[string]any{"title": req.Request.ToolName, "toolName": req.Request.ToolName, "kind": kind, "rawInput": req.Request.Input}
		if cmd, ok := req.Request.Input["command"].(string); ok {
			call["command"] = cmd
		}
		raw, _ := json.Marshal(call)
		_, allow = rejectMissing([]Option{{ID: "allow"}, {ID: "deny"}}, decide, raw)
	}
	optionID := "deny"
	if allow {
		decision = map[string]any{"behavior": "allow", "updatedInput": req.Request.Input}
		optionID = "allow"
	}
	err := writeJSON(w, map[string]any{
		"type":     "control_response",
		"response": map[string]any{"subtype": "success", "request_id": req.RequestID, "response": decision},
	})
	return Event{Kind: "permission", Body: optionID, OptionID: optionID}, err
}
