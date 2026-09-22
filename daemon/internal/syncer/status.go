package syncer

import (
	"encoding/json"
	"os"
	"time"

	"discodrive.org/daemon/internal/engine"
)

// State represents the daemon's current state.
type State string

const (
	StateIdle    State = "idle"
	StateSyncing State = "syncing"
	StateOffline State = "offline"
)

// Status is the daemon's current status, serialised to status.json.
type Status struct {
	Activity  engine.Activity `json:"activity"`
	State     State           `json:"state"`
	LastSync  time.Time       `json:"last_sync,omitempty"`
	ErrorKind string          `json:"error_kind,omitempty"`
	LastError string          `json:"last_error,omitempty"`
	Pid       int             `json:"pid"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ReadStatus reads status.json at the given path.
func ReadStatus(path string) (Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{}, err
	}
	var st Status
	err = json.Unmarshal(b, &st)
	return st, err
}

// writeStatus atomically writes the status: first to a temp file, then renames it.
func (s *Syncer) writeStatus(st Status) {
	st.Pid = os.Getpid()
	st.UpdatedAt = time.Now()
	if s.observer != nil {
		s.observer(st)
	}
	if s.statusPath == "" {
		return
	}

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		// should never happen in practice, but guard anyway
		return
	}

	tmp := s.statusPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		// log but don't crash — status writes are non-critical
		return
	}
	if err := os.Rename(tmp, s.statusPath); err != nil {
		_ = os.Remove(tmp)
	}
}
