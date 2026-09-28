package env

// CaptureLimit bounds one stored setup, resume, or service capture (#334).
// A longer capture keeps its last CaptureLimit bytes.
const CaptureLimit = 1 << 20

// Capture kinds.
const (
	CaptureSetup   = "setup"
	CaptureResume  = "resume"
	CaptureService = "service"
)

// Capture is the combined stdout and stderr of one setup, resume, or
// declared-service start. Ran is false when the hook file is absent.
// Name is the service name for CaptureService and empty otherwise.
type Capture struct {
	Kind      string
	Name      string
	Output    []byte
	Truncated bool
	Failed    bool
	Ran       bool
}

// OutputExecer runs a command in a container and returns its bounded
// combined stdout and stderr.
type OutputExecer interface {
	ExecOutput(id string, cmd []string) (out []byte, truncated bool, err error)
}

// tailBuffer keeps the last limit bytes written to it. Memory stays
// under twice the limit.
type tailBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tailBuffer) result() ([]byte, bool) {
	if len(t.buf) > t.limit {
		return t.buf[len(t.buf)-t.limit:], true
	}
	return t.buf, t.truncated
}

// capture runs cmd and keeps its output when the runtime can return it.
func (c Container) capture(handle, kind, name string, cmd []string) (Capture, error) {
	out := Capture{Kind: kind, Name: name, Ran: true}
	var err error
	if x, ok := c.RT.(OutputExecer); ok {
		out.Output, out.Truncated, err = x.ExecOutput(handle, cmd)
	} else {
		err = c.RT.Exec(handle, cmd)
	}
	out.Failed = err != nil
	return out, err
}
