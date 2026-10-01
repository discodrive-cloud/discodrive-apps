package index

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// PendingName preserves a planned upload's server name across a partial push and
// restart. It is not a synced node: scans still read the current file contents.
type PendingName struct {
	LocalPath   string
	RelPath     string
	SourcePath  string
	IsDir       bool
	Hash        string
	HeldSources []string
}

func (i *Index) PendingNames() ([]PendingName, error) {
	v, err := i.meta("pending_names")
	if err != nil || v == "" {
		return nil, err
	}
	var names []PendingName
	err = json.Unmarshal([]byte(v), &names)
	return names, err
}

// SetPendingNames atomically replaces the plan before any remote mutations.
// The next scan drops entries already indexed, removed locally, or of another type.
func (i *Index) SetPendingNames(names []PendingName) error {
	v, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return i.setMeta("pending_names", string(v))
}

// RecordPush commits the new node, its ordering and completion of its naming plan
// together. A restart cannot expose a synced node with a stale pending name.
func (i *Index) RecordPush(n Node, seq int64) error {
	return i.Batch(func(b *Batch) error {
		if err := b.Put(n); err != nil {
			return err
		}
		if _, err := b.tx.Exec("UPDATE nodes SET seq = ? WHERE node_id = ?", seq, n.NodeID); err != nil {
			return err
		}
		local := n.LocalPath
		if local == "" {
			local = n.RelPath
		}
		return b.completePendingName(local)
	})
}

// CompletePendingName handles successful requests that do not write a node
// (for example a file saved as a conflict copy).
func (i *Index) CompletePendingName(local string) error {
	return i.Batch(func(b *Batch) error { return b.completePendingName(local) })
}

func (b *Batch) completePendingName(local string) error {
	var v string
	if err := b.tx.QueryRow("SELECT value FROM meta WHERE key = 'pending_names'").Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var names []PendingName
	if err := json.Unmarshal([]byte(v), &names); err != nil {
		return err
	}
	kept := names[:0]
	for _, n := range names {
		if n.LocalPath != local {
			kept = append(kept, n)
		}
	}
	if len(kept) == len(names) {
		return nil
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	_, err = b.tx.Exec("UPDATE meta SET value = ? WHERE key = 'pending_names'", string(data))
	return err
}
