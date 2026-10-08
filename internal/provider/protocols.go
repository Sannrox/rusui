package provider

import (
	"bufio"
	"encoding/json"
	"errors"
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
	return readProviderLoop(r, rw, opened.Result.SessionID, decide, "grok", turn.Observe)
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
	res, err := readProviderLoop(r, rw, init.Session, decide, "claude", turn.Observe)
	if err != nil {
		return Result{}, err
	}
	if res.Cursor == "" {
		res.Cursor = init.Session
	}
	return res, nil
}

// runCodex uses the app-server JSON-RPC contract, not an ACP version handshake.
func runCodex(rw io.ReadWriteCloser, _ Instance, turn Turn, decide Decide) (Result, error) {
	r := bufio.NewReader(rw)
	if err := writeJSON(rw, map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "rusui", "title": "rusui", "version": "0"}}}); err != nil {
		return Result{}, err
	}
	var init struct {
		UserAgent string `json:"userAgent"`
	}
	if err := codexResponse(r, 1, &init); err != nil {
		return Result{}, err
	}
	if init.UserAgent == "" {
		return Result{}, fmt.Errorf("provider: codex initialize refused")
	}
	if err := writeJSON(rw, map[string]any{"method": "initialized"}); err != nil {
		return Result{}, err
	}
	if turn.APIKey != "" {
		if err := writeJSON(rw, map[string]any{"id": 4, "method": "account/login/start", "params": map[string]string{"type": "apiKey", "apiKey": turn.APIKey}}); err != nil {
			return Result{}, err
		}
		var login struct {
			Type string `json:"type"`
		}
		if err := codexResponse(r, 4, &login); err != nil {
			return Result{}, err
		}
		if login.Type != "apiKey" {
			return Result{}, fmt.Errorf("provider: codex API-key login refused")
		}
	}
	method := "thread/start"
	// Override account/workspace defaults. The native sandbox remains read-only;
	// write or network escalation must ask the plane and cannot persist approvals.
	params := map[string]any{"cwd": turn.Workspace, "approvalPolicy": "untrusted", "sandbox": "read-only"}
	if turn.ModelBaseURL != "" {
		base := strings.TrimRight(turn.ModelBaseURL, "/")
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		params["config"] = map[string]any{"openai_base_url": base}
	}
	if turn.Model != "" {
		params["model"] = turn.Model
	}
	if turn.Cursor != "" {
		method = "thread/resume"
		params["threadId"] = turn.Cursor
	}
	if err := writeJSON(rw, map[string]any{"id": 2, "method": method, "params": params}); err != nil {
		return Result{}, err
	}
	var opened struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := codexResponse(r, 2, &opened); err != nil {
		var rpcErr *codexRPCError
		if method != "thread/resume" || !errors.As(err, &rpcErr) {
			return Result{}, err
		}
		// Environment replacement can remove the provider's stored thread.
		// A new thread uses the same cwd, sandbox, policy and turn grant.
		delete(params, "threadId")
		if err := writeJSON(rw, map[string]any{"id": 5, "method": "thread/start", "params": params}); err != nil {
			return Result{}, err
		}
		if err := codexResponse(r, 5, &opened); err != nil {
			return Result{}, err
		}
	}
	if opened.Thread.ID == "" {
		return Result{}, fmt.Errorf("provider: codex thread missing")
	}
	if err := writeJSON(rw, map[string]any{"id": 3, "method": "turn/start", "params": map[string]any{"threadId": opened.Thread.ID, "input": []map[string]string{{"type": "text", "text": turn.Prompt}}, "cwd": turn.Workspace, "approvalPolicy": "untrusted", "sandboxPolicy": map[string]any{"type": "readOnly"}}}); err != nil {
		return Result{}, err
	}
	return readProviderLoop(r, rw, opened.Thread.ID, decide, "codex", turn.Observe)
}

type codexRPCError struct {
	Code int `json:"code"`
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("provider: codex request refused (%d)", e.Code)
}

func codexResponse(r *bufio.Reader, id int, out any) error {
	for {
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *codexRPCError  `json:"error"`
		}
		if err := readJSON(r, &msg); err != nil {
			return err
		}
		if msg.ID == nil {
			continue
		}
		if *msg.ID != id {
			return fmt.Errorf("provider: codex response id mismatch")
		}
		if msg.Error != nil {
			return msg.Error
		}
		return json.Unmarshal(msg.Result, out)
	}
}

// answerCodexRequest never returns a session-wide grant or a policy amendment.
func answerCodexRequest(w io.Writer, msg map[string]any, decide Decide) (Event, error) {
	method, _ := msg["method"].(string)
	params, _ := msg["params"].(map[string]any)
	if method == "item/commandExecution/requestApproval" && params["networkApprovalContext"] != nil {
		// A shell execution allowance is not managed network authorization.
		err := writeJSON(w, map[string]any{"id": msg["id"], "result": map[string]string{"decision": "decline"}})
		return Event{Kind: "permission", OptionID: "decline", Body: "decline"}, err
	}
	if method == "item/permissions/requestApproval" {
		err := writeJSON(w, map[string]any{"id": msg["id"], "result": map[string]any{"permissions": map[string]any{}, "scope": "turn"}})
		return Event{Kind: "permission", OptionID: "deny", Body: "deny"}, err
	}
	if method != "item/commandExecution/requestApproval" && method != "item/fileChange/requestApproval" {
		return Event{}, fmt.Errorf("provider: unsupported codex request %q", method)
	}
	title, kind := "fileChange", "edit"
	command, _ := params["command"].(string)
	if method == "item/commandExecution/requestApproval" {
		title, kind = "commandExecution", "execute"
	}
	raw, _ := json.Marshal(map[string]any{"toolName": title, "title": title, "kind": kind, "command": command})
	decision := "decline"
	if decide != nil {
		option, allow := decide([]Option{{ID: "accept"}, {ID: "decline"}}, raw)
		if allow && option == "accept" {
			decision = "accept"
		}
	}
	err := writeJSON(w, map[string]any{"id": msg["id"], "result": map[string]string{"decision": decision}})
	return Event{Kind: "permission", OptionID: decision, Body: decision}, err
}

func readProviderLoop(r *bufio.Reader, w io.Writer, cursor string, decide Decide, kind string, observe func(Event)) (Result, error) {
	var res Result
	res.Cursor = cursor
	emit := func(ev Event) {
		res.Events = append(res.Events, ev)
		if observe != nil {
			observe(ev)
		}
	}
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return res, err
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			return res, err
		}
		if kind == "codex" {
			if msg["error"] != nil {
				return res, fmt.Errorf("provider: codex request failed")
			}
			if msg["id"] != nil && msg["method"] != nil {
				ev, err := answerCodexRequest(w, msg, decide)
				if err != nil {
					return res, err
				}
				emit(ev)
				continue
			}
			if msg["id"] != nil && msg["result"] != nil {
				continue
			} // turn/start acknowledges before completion
			if msg["method"] == "turn/completed" {
				if params, ok := msg["params"].(map[string]any); ok {
					if turn, ok := params["turn"].(map[string]any); ok && turn["status"] != "completed" {
						return res, fmt.Errorf("provider: codex turn did not complete")
					}
				}
			}
		}
		if kind == "claude" && msg["type"] == "control_request" {
			ev, err := answerClaudeControl(w, line, decide)
			if err != nil {
				return res, err
			}
			emit(ev)
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
			replyID := msg["id"]
			if !allow {
				optionID = "deny"
			}
			if err := writePermission(w, kind, replyID, optionID, allow); err != nil {
				return res, err
			}
			emit(Event{Kind: "permission", Body: optionID, OptionID: optionID})
			continue
		}
		if msg["type"] == "rate_limit_event" || msg["method"] == "item/usageLimit" {
			reset, _ := msg["resets_at"].(string)
			ev := Event{Kind: "usage_limit", Reset: reset, ResetMissing: reset == ""}
			emit(ev)
			continue
		}
		if msg["method"] == "item/question" {
			params, _ := msg["params"].(map[string]any)
			id, _ := params["id"].(string)
			body, _ := params["body"].(string)
			emit(Event{Kind: "question", Body: body, OptionID: id})
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
			// Observed only: Result.Events keeps no transcript bodies, so a
			// long turn does not hold them all (#344).
			if observe != nil {
				for _, ev := range claudeStreamEvents(line) {
					observe(ev)
				}
			}
			continue
		}
		if kind == "codex" {
			if observe != nil {
				for _, ev := range codexStreamEvents(line) {
					observe(ev)
				}
			}
			continue
		}
		text := string(line)
		kindName := "transcript"
		if strings.Contains(text, "tool_call") || strings.Contains(text, "tool_use") || msg["method"] == "item/tool" {
			kindName = "tool_call"
		}
		emit(Event{Kind: kindName, Body: strings.TrimSpace(text)})
		if msg["type"] == "result" {
			return res, nil
		}
	}
}

// codexStreamEvents selects transcript-bearing app-server notifications.
// Completed messages avoid duplicating the same text in deltas and items.
// Tool results retain status only, as with Claude and ACP.
func codexStreamEvents(line []byte) []Event {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Text string `json:"text"`
			Item struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Text    string `json:"text"`
				Command string `json:"command"`
				Tool    string `json:"tool"`
				Status  string `json:"status"`
			} `json:"item"`
		} `json:"params"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return nil
	}
	if msg.Method == "item/agentMessage" {
		return []Event{{Kind: "transcript", Body: msg.Params.Text}}
	}
	if msg.Method != "item/started" && msg.Method != "item/completed" {
		return nil
	}
	item := msg.Params.Item
	if item.Type == "agentMessage" {
		if msg.Method == "item/completed" {
			return []Event{{Kind: "transcript", Body: item.Text}}
		}
		return nil
	}
	tool := &ToolCall{ID: item.ID, Name: item.Type, Kind: "other", Status: "pending"}
	switch item.Type {
	case "commandExecution":
		tool.Kind = "execute"
		tool.Input = map[string]string{"command": item.Command}
	case "fileChange":
		tool.Kind = "edit"
	case "mcpToolCall", "dynamicToolCall":
		tool.Name = item.Tool
	case "webSearch":
		tool.Kind = "search"
	default:
		return nil
	}
	if msg.Method == "item/completed" {
		tool.Status = "completed"
		if item.Status == "failed" || item.Status == "declined" {
			tool.Status = "failed"
		}
	}
	return []Event{{Kind: "tool_call", Tool: tool}}
}

func writePermission(w io.Writer, kind string, id any, optionID string, allow bool) error {
	if !allow {
		optionID = "deny"
	}
	switch kind {
	case "codex":
		return writeJSON(w, map[string]any{"id": id, "result": map[string]any{"optionId": optionID}})
	default:
		return writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}})
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

// claudeInputFields are the tool input fields a transcript keeps: what a
// call runs or where it looks. File bodies, edit strings, and prompts are
// left out; they can be large and can carry secrets. The recorder redacts
// and bounds what is kept.
var claudeInputFields = []string{
	"command", "description", "file_path", "notebook_path", "path",
	"pattern", "glob", "url", "query", "subagent_type", "skill", "title",
}

// claudeStreamEvents is the transcript of one Claude Code stream-json
// line. An assistant message's text blocks are transcript events and its
// tool_use blocks are pending tool calls; a user message's tool_result
// blocks are those calls' outcomes, without the result content. Thinking
// and any other line are not recorded.
func claudeStreamEvents(line []byte) []Event {
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil || (msg.Type != "assistant" && msg.Type != "user") {
		return nil
	}
	var blocks []struct {
		Type      string         `json:"type"`
		Text      string         `json:"text"`
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id"`
		IsError   bool           `json:"is_error"`
	}
	// A user message's content may be a plain string: nothing to record.
	if err := json.Unmarshal(msg.Message.Content, &blocks); err != nil {
		return nil
	}
	var out []Event
	for _, b := range blocks {
		switch {
		case msg.Type == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
			out = append(out, Event{Kind: "transcript", Body: b.Text})
		case msg.Type == "assistant" && b.Type == "tool_use":
			kind, ok := claudeToolKinds[b.Name]
			if !ok {
				kind = "other"
			}
			out = append(out, Event{Kind: "tool_call", Body: b.Name, Tool: &ToolCall{
				ID: b.ID, Name: b.Name, Kind: kind, Input: claudeInputSummary(b.Input), Status: "pending",
			}})
		case msg.Type == "user" && b.Type == "tool_result":
			status := "completed"
			if b.IsError {
				status = "failed"
			}
			out = append(out, Event{Kind: "tool_call", Body: status, Tool: &ToolCall{ID: b.ToolUseID, Status: status}})
		}
	}
	return out
}

func claudeInputSummary(input map[string]any) map[string]string {
	out := map[string]string{}
	for _, key := range claudeInputFields {
		v, ok := input[key].(string)
		if !ok || v == "" {
			continue
		}
		out[key] = v
	}
	return out
}
