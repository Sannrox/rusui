package store

import "time"

// First-class environment-plane identities. A review job is a turn on a
// session; GitHub (repo, item) is a source attribute of that session.

type Environment struct {
	ID          int64
	Name        string
	Driver      string
	State       string
	Handle      string
	SourceHash  string
	ExpiresAt   *time.Time
	SleptAt     *time.Time
	CPUMillis   int
	MemoryBytes int64
	CreatedAt   time.Time
}

const (
	EnvReady    = "ready"
	EnvSleeping = "sleeping"
	EnvExpired  = "expired"
	EnvTTL      = 72 * time.Hour
)

type Runner struct {
	ID         int64
	Name       string
	Kind       string
	State      string
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

type Session struct {
	ID               int64
	EnvironmentID    int64
	EnvironmentState string `json:"environment_state"`
	Kind             string
	Repo             string
	Item             int
	ItemKind         string
	State            string
	Project          string
	Prompt           string
	GuestSessionID   string
	CreatedAt        time.Time
}

type EnvironmentReceipt struct {
	ID            int64     `json:"id"`
	EnvironmentID int64     `json:"environment_id"`
	SessionID     *int64    `json:"session_id,omitempty"`
	Kind          string    `json:"kind"`
	State         string    `json:"state"`
	Detail        string    `json:"detail,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Turn struct {
	ID                  int64
	SessionID           int64
	Lane                string
	PendingRevision     int
	ClaimedRevision     int
	LeaseGeneration     int
	LeaseExpiresAt      *time.Time
	ExecutionDeadlineAt *time.Time
	RetryCount          int
	State               string
}

// Process is a durable Rusui identity for one Sumika process generation.
// State is the last accepted observation, not proof of the process's live state.
type Process struct {
	ID                int64      `json:"id"`
	SessionID         int64      `json:"session_id"`
	Generation        int64      `json:"generation"`
	Runtime           string     `json:"runtime"`
	Name              string     `json:"name"`
	IdentityHash      string     `json:"-"`
	State             string     `json:"state"`
	Revision          int64      `json:"revision"`
	CreatedAt         time.Time  `json:"created_at"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	Attaches          []Attach   `json:"attaches"`
}

// Attach is a durable identity/status observation for a temporary Sumika
// Attach. Sumika remains authoritative for the live connection.
type Attach struct {
	ID                int64      `json:"id"`
	ProcessID         int64      `json:"process_id"`
	ProcessGeneration int64      `json:"process_generation"`
	Generation        int64      `json:"generation"`
	Revision          int64      `json:"revision"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
}

type Event struct {
	ID         int64
	SessionID  *int64
	Source     string
	DeliveryID string
	Repo       string
	Item       int
	ItemKind   string
	ReceivedAt time.Time
}

type Action struct {
	ID               string
	SessionID        *int64
	TurnID           *int64
	ReviewRevisionID *int64
	Repo             string
	Item             int
	Type             string
	ReasonCode       string
	EvidenceClass    string
	LimitSentence    string
	Body             string
}

const (
	LocalEnvironmentName = "local"
	LocalRunnerName      = "local"
	SessionKindReview    = "review"
	SessionKindRun       = "run"
	SessionKindScheduled = "scheduled"
	SessionKindLocal     = "local"
	DefaultEnvironmentID = int64(1)
)

const (
	ProcessRuntimeSumika = "sumika"
	ProcessStarting      = "starting"
	ProcessRunning       = "running"
	ProcessIdle          = "idle"
	ProcessBlocked       = "blocked"
	ProcessDead          = "dead"
	ProcessLost          = "lost"
	ProcessUnknown       = "unknown"

	AttachAttached      = "attached"
	AttachDetached      = "detached"
	AttachStolen        = "stolen"
	AttachProcessExited = "process_exited"
	AttachUnknown       = "unknown"
)

type Schedule struct {
	ID           int64
	Project      string
	Name         string
	EverySeconds int
	Prompt       string
}
