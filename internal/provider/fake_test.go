package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if kind := os.Getenv("RUSUI_PROVIDER_FAKE"); kind != "" {
		if err := serveFake(kind, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func serveFake(kind string, in io.Reader, out io.Writer) error {
	if os.Getenv("RUSUI_PROVIDER_FAKE_MODE") == "probe" {
		ver, err := PinnedVersion(kind)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, ver)
		return err
	}
	switch kind {
	case KindGrok:
		return fakeGrok(in, out)
	case KindClaude:
		return fakeClaude(in, out)
	case KindCodex:
		return fakeCodex(in, out)
	default:
		return fmt.Errorf("fake %s", kind)
	}
}

func readMap(r *bufio.Reader) (map[string]any, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func fakeGrok(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	init, err := readMap(r)
	if err != nil {
		return err
	}
	params, _ := init["params"].(map[string]any)
	version, _ := params["protocolVersion"].(float64)
	if int(version) != GrokACPVersion {
		return writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{"protocolVersion": int(version)}})
	}
	if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{"protocolVersion": GrokACPVersion}}); err != nil {
		return err
	}
	open, err := readMap(r)
	if err != nil {
		return err
	}
	resume := open["method"] == "session/load"
	if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": open["id"], "result": map[string]any{"sessionId": "grok-sess"}}); err != nil {
		return err
	}
	if _, err := readMap(r); err != nil {
		return err
	}
	if resume {
		if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"text": "resumed"}}); err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": 3, "result": map[string]any{"stopReason": "end_turn"}})
	}
	if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionUpdate": "tool_call", "title": "shell"}}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": "perm-1", "method": "session/request_permission", "params": map[string]any{"options": []map[string]string{{"id": "allow-once"}}}}); err != nil {
		return err
	}
	reply, err := readMap(r)
	if err != nil {
		return err
	}
	if permissionAllows(reply) {
		return fmt.Errorf("fake grok was allowed without a reject option")
	}
	if err := writeJSON(out, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"text": "hello-grok"}}); err != nil {
		return err
	}
	return writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": 3, "result": map[string]any{"stopReason": "end_turn"}})
}

func fakeClaude(in io.Reader, out io.Writer) error {
	if err := writeJSON(out, map[string]any{
		"type": "system", "subtype": "init", "protocol": ClaudeStreamProto,
		"claude_code_version": ClaudeCodeVersion, "session_id": "claude-sess",
	}); err != nil {
		return err
	}
	r := bufio.NewReader(in)
	user, err := readMap(r)
	if err != nil {
		return err
	}
	msg, _ := user["message"].(map[string]any)
	content, _ := msg["content"].(string)
	resume := content == "resume"
	if resume {
		if err := writeJSON(out, map[string]any{"type": "assistant", "text": "resumed"}); err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"type": "result"})
	}
	if err := writeJSON(out, map[string]any{"type": "assistant", "text": "hello-claude"}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"type": "assistant", "tool_use": "shell"}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"type": "rate_limit_event"}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"type": "permission_request", "id": "perm-1", "options": []map[string]string{{"id": "allow-once"}}}); err != nil {
		return err
	}
	reply, err := readMap(r)
	if err != nil {
		return err
	}
	if permissionAllows(reply) {
		return fmt.Errorf("fake claude was allowed without a reject option")
	}
	return writeJSON(out, map[string]any{"type": "result"})
}

func fakeCodex(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	init, err := readMap(r)
	if err != nil {
		return err
	}
	params, _ := init["params"].(map[string]any)
	if params["protocolVersion"] != CodexAppServerProto {
		return writeJSON(out, map[string]any{"id": init["id"], "result": map[string]any{"protocolVersion": params["protocolVersion"]}})
	}
	if err := writeJSON(out, map[string]any{"id": init["id"], "result": map[string]any{"protocolVersion": CodexAppServerProto}}); err != nil {
		return err
	}
	open, err := readMap(r)
	if err != nil {
		return err
	}
	resume := open["method"] == "thread/resume"
	if err := writeJSON(out, map[string]any{"id": open["id"], "result": map[string]any{"threadId": "codex-thread"}}); err != nil {
		return err
	}
	if _, err := readMap(r); err != nil {
		return err
	}
	if resume {
		if err := writeJSON(out, map[string]any{"method": "item/agentMessage", "params": map[string]any{"text": "resumed"}}); err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"method": "turn/completed"})
	}
	if err := writeJSON(out, map[string]any{"method": "item/tool", "params": map[string]any{"title": "shell"}}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"method": "item/question", "params": map[string]any{"id": "q1", "body": "which file"}}); err != nil {
		return err
	}
	if err := writeJSON(out, map[string]any{"id": "perm-1", "method": "item/permission", "params": map[string]any{"options": []map[string]string{{"id": "allow-once"}}}}); err != nil {
		return err
	}
	reply, err := readMap(r)
	if err != nil {
		return err
	}
	if permissionAllows(reply) {
		return fmt.Errorf("fake codex was allowed without a reject option")
	}
	if err := writeJSON(out, map[string]any{"method": "item/agentMessage", "params": map[string]any{"text": "hello-codex"}}); err != nil {
		return err
	}
	return writeJSON(out, map[string]any{"method": "turn/completed"})
}

func permissionAllows(reply map[string]any) bool {
	raw, _ := json.Marshal(reply)
	text := string(raw)
	return strings.Contains(text, "allow") && !strings.Contains(text, "deny") && !strings.Contains(text, "reject")
}
