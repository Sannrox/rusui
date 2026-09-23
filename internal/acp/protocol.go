package acp

import "encoding/json"

// GrokStdio is the default guest spawn (ADR 0002).
const GrokStdio = "agent --permission-mode default agent stdio"

// ProtocolVersion is the ACP version the in-tree editor shim speaks.
const ProtocolVersion = 1

// EditorAgentName is the first supported editor-facing ACP agent.
const EditorAgentName = "rusui-acp"

const (
	MethodInitialize          = "initialize"
	MethodSessionNew          = "session/new"
	MethodSessionLoad         = "session/load"
	MethodSessionPrompt       = "session/prompt"
	MethodSessionCancel       = "session/cancel"
	MethodSessionUpdate       = "session/update"
	MethodRequestPermission   = "session/request_permission"
	MethodFSReadTextFile      = "fs/read_text_file"
	MethodFSWriteTextFile     = "fs/write_text_file"
	MethodTerminalCreate      = "terminal/create"
	MethodTerminalOutput      = "terminal/output"
	MethodTerminalWaitForExit = "terminal/wait_for_exit"
	MethodTerminalKill        = "terminal/kill"
	MethodTerminalRelease     = "terminal/release"
)

const (
	ActionFSRead          = "acp.fs.read_text_file"
	ActionFSWrite         = "acp.fs.write_text_file"
	ActionTerminalCreate  = "acp.terminal.create"
	ActionTerminalOutput  = "acp.terminal.output"
	ActionTerminalWait    = "acp.terminal.wait_for_exit"
	ActionTerminalKill    = "acp.terminal.kill"
	ActionTerminalRelease = "acp.terminal.release"
	ActionPermission      = "acp.session.request_permission"
	ActionUpdate          = "acp.session.update"
	ActionApproval        = "acp.approval"
	ActionUnknown         = "acp.unknown"
)

const (
	ReasonRecorded   = "recorded"
	ReasonDenied     = "denied"
	ReasonAllowed    = "allowed"
	ReasonUnmatched  = "permission_unmatched"
	EvidenceObserved = "plane_observed"
	LimitSentence    = "ACP client recorded the request; no request bypasses stdio dispatch."
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type InitializeParams struct {
	ProtocolVersion    int            `json:"protocolVersion"`
	ClientCapabilities map[string]any `json:"clientCapabilities"`
	ClientInfo         map[string]any `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion int            `json:"protocolVersion"`
	AgentInfo       map[string]any `json:"agentInfo"`
}

type SessionNewParams struct {
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

type SessionIDResult struct {
	SessionID string `json:"sessionId"`
}

type SessionLoadParams struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd,omitempty"`
}

type PromptParams struct {
	SessionID string        `json:"sessionId"`
	Prompt    []PromptBlock `json:"prompt"`
}

type PromptBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type PermissionParams struct {
	SessionID string          `json:"sessionId"`
	ToolCall  json.RawMessage `json:"toolCall"`
	Options   []PermOption    `json:"options"`
}

type PermOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type PermissionOutcome struct {
	Outcome PermissionSelected `json:"outcome"`
}

type PermissionSelected struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId"`
}

type FSReadParams struct {
	Path string `json:"path"`
}

type FSReadResult struct {
	Content string `json:"content"`
}

type FSWriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type TerminalCreateParams struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
}

type TerminalIDResult struct {
	TerminalID string `json:"terminalId"`
}

type TerminalRef struct {
	TerminalID string `json:"terminalId"`
}

type SessionUpdateParams struct {
	SessionID string         `json:"sessionId"`
	Update    map[string]any `json:"update"`
}
