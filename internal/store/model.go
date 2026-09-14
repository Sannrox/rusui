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
	ID            int64
	EnvironmentID int64
	Kind          string
	Repo          string
	Item          int
	ItemKind      string
	State         string
	CreatedAt     time.Time
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
	DefaultEnvironmentID = int64(1)
)
