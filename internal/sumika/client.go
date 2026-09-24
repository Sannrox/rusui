package sumika

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const responseLimit = 8 << 20

type Status string

const (
	StatusRunning Status = "running"
	StatusIdle    Status = "idle"
	StatusBlocked Status = "blocked"
	StatusDead    Status = "dead"
	StatusUnknown Status = "unknown"
)

type Session struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
	Project *string  `json:"project"`
	Status  Status   `json:"status"`
	Focused bool     `json:"focused"`
}

type Client interface {
	Start(name string, argv []string, cwd, project string) (Session, error)
	List() ([]Session, error)
	Kill(name string, force bool) (Session, error)
	Attach(name string) (Session, net.Conn, error)
}

type SocketClient struct {
	Path    string
	Timeout time.Duration
}

type request struct {
	Op      string   `json:"op"`
	Name    string   `json:"name,omitempty"`
	Argv    []string `json:"argv,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Project *string  `json:"project,omitempty"`
	Force   bool     `json:"force,omitempty"`
}

type response struct {
	OK    bool `json:"ok"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Session  *Session  `json:"session"`
	Sessions []Session `json:"sessions"`
}

type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("sumika %s: %s", e.Code, e.Message)
}

func DefaultSocketPath() (string, error) {
	if path := os.Getenv("SUMIKA_SOCK"); path != "" {
		return path, nil
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "sumika", "sumika.sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sumika socket path: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "sumika", "sumika.sock"), nil
	case "linux":
		return filepath.Join(home, ".cache", "sumika", "sumika.sock"), nil
	default:
		return "", fmt.Errorf("sumika local runtime is unsupported on %s", runtime.GOOS)
	}
}

func NewSocketClient(path string) *SocketClient {
	return &SocketClient{Path: path, Timeout: 5 * time.Second}
}

func (c *SocketClient) Start(name string, argv []string, cwd, project string) (Session, error) {
	var projectValue *string
	if project != "" {
		projectValue = &project
	}
	res, err := c.rpc(request{Op: "start", Name: name, Argv: argv, Cwd: cwd, Project: projectValue})
	if err != nil {
		return Session{}, err
	}
	if res.Session == nil {
		return Session{}, errors.New("sumika response has no session")
	}
	return *res.Session, nil
}

func (c *SocketClient) List() ([]Session, error) {
	res, err := c.rpc(request{Op: "list"})
	if err != nil {
		return nil, err
	}
	if res.Sessions == nil {
		return nil, errors.New("sumika response has no sessions")
	}
	return res.Sessions, nil
}

func (c *SocketClient) Kill(name string, force bool) (Session, error) {
	res, err := c.rpc(request{Op: "kill", Name: name, Force: force})
	if err != nil {
		return Session{}, err
	}
	if res.Session == nil {
		return Session{}, errors.New("sumika response has no session")
	}
	return *res.Session, nil
}

func (c *SocketClient) Attach(name string) (Session, net.Conn, error) {
	conn, err := c.connect()
	if err != nil {
		return Session{}, nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(c.timeout())); err != nil {
		_ = conn.Close()
		return Session{}, nil, err
	}
	if err := json.NewEncoder(conn).Encode(request{Op: "attach", Name: name}); err != nil {
		_ = conn.Close()
		return Session{}, nil, err
	}
	var res response
	if err := readResponse(conn, &res); err != nil {
		_ = conn.Close()
		return Session{}, nil, err
	}
	if err := res.remoteError(); err != nil {
		_ = conn.Close()
		return Session{}, nil, err
	}
	if res.Session == nil {
		_ = conn.Close()
		return Session{}, nil, errors.New("sumika attach response has no session")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return Session{}, nil, err
	}
	return *res.Session, conn, nil
}

func (c *SocketClient) rpc(req request) (response, error) {
	conn, err := c.connect()
	if err != nil {
		return response{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(c.timeout())); err != nil {
		return response{}, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return response{}, err
	}
	var res response
	if err := readResponse(conn, &res); err != nil {
		return response{}, err
	}
	if err := res.remoteError(); err != nil {
		return response{}, err
	}
	return res, nil
}

func (c *SocketClient) connect() (*net.UnixConn, error) {
	if c.Path == "" {
		return nil, errors.New("sumika socket path is empty")
	}
	if err := sameUserSocket(c.Path); err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", c.Path, c.timeout())
	if err != nil {
		return nil, fmt.Errorf("sumika socket: %w", err)
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("sumika connection is not a Unix socket")
	}
	if err := sameUserPeer(unix); err != nil {
		_ = unix.Close()
		return nil, err
	}
	return unix, nil
}

func (c *SocketClient) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Second
}

func (r response) remoteError() error {
	if r.Error != nil {
		return &RemoteError{Code: r.Error.Code, Message: r.Error.Message}
	}
	if !r.OK {
		return errors.New("sumika request failed without an error")
	}
	return nil
}

func readResponse(conn net.Conn, out *response) error {
	line := make([]byte, 0, 1024)
	var one [1]byte
	for {
		// Attach switches to raw PTY bytes after this line, so do not buffer past its newline.
		if _, err := io.ReadFull(conn, one[:]); err != nil {
			return err
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
		if len(line) > responseLimit {
			return fmt.Errorf("sumika response exceeds %d bytes", responseLimit)
		}
	}
	if err := json.Unmarshal(line, out); err != nil {
		return fmt.Errorf("sumika response: %w", err)
	}
	return nil
}
