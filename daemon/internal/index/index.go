// Package index is the local state index for the sync client (SQLite via modernc).
package index

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS nodes (
    node_id      TEXT PRIMARY KEY,
    rel_path     TEXT NOT NULL,
    is_dir       INTEGER NOT NULL,
    version      INTEGER NOT NULL,
    content_hash TEXT,
    size         INTEGER,
    -- Where the node lives on disk when that differs from rel_path; empty means "same".
    -- See internal/localname.
    local_path   TEXT NOT NULL DEFAULT ''
);
-- The nodes as they stood before a reset that keeps the folder (see ClearKeepingSnapshot),
-- kept until the reset completes so a restart in between still knows what was synced.
CREATE TABLE IF NOT EXISTS nodes_before_reset (
    node_id      TEXT PRIMARY KEY,
    rel_path     TEXT NOT NULL,
    is_dir       INTEGER NOT NULL,
    version      INTEGER NOT NULL,
    content_hash TEXT,
    size         INTEGER,
    local_path   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS local (
    node_id TEXT PRIMARY KEY,
    state   TEXT NOT NULL,
    version INTEGER NOT NULL,
    path    TEXT NOT NULL
);`

// Node is an index record for a node known to the client.
type Node struct {
	NodeID  string
	RelPath string
	// LocalPath is where the node lives on disk, which differs from RelPath when the
	// server name contains characters the filesystem rejects (see internal/localname).
	// Reads always report it: rows that store none — every node whose name needed no
	// change, and every row written before the column existed — report RelPath.
	LocalPath   string
	IsDir       bool
	Version     int64
	ContentHash string
	Size        int64
}

type Index struct{ db *sql.DB }

// Open opens (or creates) the index and applies the schema.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite allows only one writer. Serialize all access through a single
	// connection so concurrent callers (e.g. the desktop UI firing pin/unpin/refresh
	// from separate goroutines) queue instead of failing with "database is locked".
	db.SetMaxOpenConns(1)
	// WAL + synchronous=NORMAL: without them every commit rewrites and fsyncs a rollback
	// journal, and the first pull after pairing — which applies the whole tree — spent
	// minutes on that alone, showing an empty file list the entire time. The index is a
	// cache rebuildable from /sync/changes, so trading a fsync per commit for one at
	// checkpoints costs nothing worse than re-pulling after a power loss.
	for _, pragma := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// CREATE TABLE IF NOT EXISTS leaves an index built by an earlier version untouched, so
	// the column has to be added separately. Existing rows default to empty, which reads as
	// "same as rel_path" — true for every node those versions could store.
	if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN local_path TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		db.Close()
		return nil, err
	}
	// seq: the feed seq at which the pull last applied the node (see SetSeq); 0 for rows
	// written before the column existed and for nodes only a push has written so far.
	if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}

func (i *Index) Close() error { return i.db.Close() }

// Cursor returns the last applied seq (0 if never synced).
func (i *Index) Cursor() (int64, error) {
	var v string
	err := i.db.QueryRow("SELECT value FROM meta WHERE key = 'cursor'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (i *Index) SetCursor(seq int64) error {
	_, err := i.db.Exec(setCursorSQL, strconv.FormatInt(seq, 10))
	return err
}

// ScopeEpoch returns the last scope epoch the client reconciled to (0 if never set).
func (i *Index) ScopeEpoch() (int64, error) {
	var v string
	err := i.db.QueryRow("SELECT value FROM meta WHERE key = 'scope_epoch'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// HasScopeEpoch distinguishes a new mirror from an interrupted scope rebuild.
func (i *Index) HasScopeEpoch() (bool, error) {
	v, err := i.meta("scope_epoch")
	return v != "", err
}

func (i *Index) SetScopeEpoch(epoch int64) error {
	_, err := i.db.Exec(
		"INSERT INTO meta(key, value) VALUES('scope_epoch', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		strconv.FormatInt(epoch, 10))
	return err
}

// ServerURL returns the server this index was built against ("" if never set).
// Used to detect re-pairing to a different server, where the whole index is stale.
func (i *Index) ServerURL() (string, error) {
	var v string
	err := i.db.QueryRow("SELECT value FROM meta WHERE key = 'server_url'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func (i *Index) SetServerURL(u string) error {
	_, err := i.db.Exec(
		"INSERT INTO meta(key, value) VALUES('server_url', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", u)
	return err
}

// MirrorReady reports whether the sync folder has been established as this index's mirror,
// i.e. a pull from the server has completed since the index was created or cleared. Until
// then the folder's contents are whatever was there before — not something to upload.
//
// An index written by a version that did not record this is taken as ready when it has
// pulled anything at all, so an upgrade does not treat every existing mirror as foreign.
func (i *Index) MirrorReady() (bool, error) {
	v, err := i.meta("mirror_ready")
	if err != nil {
		return false, err
	}
	if v != "" {
		return v == "1", nil
	}
	cursor, err := i.Cursor()
	return cursor > 0, err
}

func (i *Index) SetMirrorReady(ready bool) error {
	v := "0"
	if ready {
		v = "1"
	}
	return i.setMeta("mirror_ready", v)
}

// KeepLocalOnce is the one-shot allowance a reset-in-place leaves behind: the next pull
// keeps the folder's files as the mirror instead of setting them aside.
func (i *Index) KeepLocalOnce() (bool, error) {
	v, err := i.meta("keep_local_once")
	return v == "1", err
}

func (i *Index) SetKeepLocalOnce(keep bool) error {
	v := "0"
	if keep {
		v = "1"
	}
	return i.setMeta("keep_local_once", v)
}

func (i *Index) meta(key string) (string, error) {
	var v string
	err := i.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (i *Index) setMeta(key, value string) error {
	_, err := i.db.Exec(
		"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// BindMirrorPairing makes a retained database safe for a newly paired device. A missing
// fingerprint is also untrusted: the old schema cannot prove whose mirror it describes.
// Reset the index and the identity atomically, before any pass may push local files.
func (i *Index) BindMirrorPairing(fingerprint string) error {
	if fingerprint == "" {
		return errors.New("empty mirror pairing")
	}
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var previous string
	err = tx.QueryRow("SELECT value FROM meta WHERE key = 'mirror_pairing'").Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == fingerprint {
		return tx.Commit()
	}
	for _, statement := range []string{"DELETE FROM nodes", "DELETE FROM nodes_before_reset", "DELETE FROM local", "DELETE FROM meta"} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	for _, kv := range [][2]string{{"mirror_pairing", fingerprint}, {"mirror_ready", "0"}, {"cursor", "0"}} {
		if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?)", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Clear drops all known nodes, resets the cursor to 0 and marks the mirror as not yet
// established, so the next pull rebuilds the tree from scratch and treats the folder as
// it would after a pairing, and drops any pre-reset snapshot. A reset that keeps the
// folder (scope change, index recovery) uses ClearKeepingSnapshot instead. The
// scope_epoch is left untouched (the caller sets it after a successful reconcile).
func (i *Index) Clear() error { return i.clear(false) }

// ClearKeepingSnapshot is Clear for a reset that keeps the folder as the mirror: the node
// rows move to nodes_before_reset instead of being dropped, so the later sweep can tell
// synced files from the user's own even across a restart. A snapshot that is already
// there wins — it is what the folder held before the first attempt; the live rows are a
// partial re-pull by then — so a repeat only drops them.
func (i *Index) ClearKeepingSnapshot() error { return i.clear(true) }

func (i *Index) clear(keep bool) error {
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back only if Commit didn't run
	if keep {
		var held int
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes_before_reset)").Scan(&held); err != nil {
			return err
		}
		if held == 0 {
			if _, err := tx.Exec(`INSERT INTO nodes_before_reset
				(node_id, rel_path, is_dir, version, content_hash, size, local_path)
				SELECT node_id, rel_path, is_dir, version, content_hash, size, local_path FROM nodes`); err != nil {
				return err
			}
		}
	} else if _, err := tx.Exec("DELETE FROM nodes_before_reset"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM nodes"); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"cursor", "0"}, {"mirror_ready", "0"}, {"pending_names", "null"}} {
		if _, err := tx.Exec(
			"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BeforeReset returns the snapshot ClearKeepingSnapshot took, or none if no reset is
// pending.
func (i *Index) BeforeReset() ([]Node, error) { return i.nodesFrom("nodes_before_reset") }

// ClearBeforeReset drops the snapshot once the reset it belongs to has completed, or once
// a completed pull shows the folder was reconciled another way.
func (i *Index) ClearBeforeReset() error {
	_, err := i.db.Exec("DELETE FROM nodes_before_reset")
	return err
}

func (i *Index) Get(nodeID string) (Node, bool, error) {
	var n Node
	var isDir int
	var hash sql.NullString
	err := i.db.QueryRow(
		"SELECT node_id, rel_path, is_dir, version, content_hash, size, IIF(local_path = '', rel_path, local_path) FROM nodes WHERE node_id = ?", nodeID).
		Scan(&n.NodeID, &n.RelPath, &isDir, &n.Version, &hash, &n.Size, &n.LocalPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	n.IsDir = isDir != 0
	n.ContentHash = hash.String
	return n, true, nil
}

// GetByPath returns the node at the given rel_path (slash-relative), if present.
func (i *Index) GetByPath(relPath string) (Node, bool, error) {
	var n Node
	var isDir int
	var hash sql.NullString
	err := i.db.QueryRow(
		"SELECT node_id, rel_path, is_dir, version, content_hash, size, IIF(local_path = '', rel_path, local_path) FROM nodes WHERE rel_path = ?", relPath).
		Scan(&n.NodeID, &n.RelPath, &isDir, &n.Version, &hash, &n.Size, &n.LocalPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	n.IsDir = isDir != 0
	n.ContentHash = hash.String
	return n, true, nil
}

// Batch applies many changes in a single transaction.
//
// A page of the change feed is thousands of rows; applied one statement at a time each one
// commits on its own, and on a phone that is a fsync apiece — the first pull after pairing
// spent minutes there. It is also the correct unit: the cursor may only advance once the rows
// it covers are in, so a page that fails partway must leave nothing behind.
//
// The batch is rolled back if fn returns an error, and fn's error is returned as-is.
func (i *Index) Batch(fn func(*Batch) error) error {
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back only if Commit didn't run
	if err := fn(&Batch{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// Batch is the write side of [Index.Batch]: the same operations, inside one transaction.
type Batch struct{ tx *sql.Tx }

func (b *Batch) Put(n Node) error {
	_, err := b.tx.Exec(putNodeSQL,
		n.NodeID, n.RelPath, boolToInt(n.IsDir), n.Version, n.ContentHash, n.Size, storedLocalPath(n))
	return err
}

func (b *Batch) Delete(nodeID string) error {
	_, err := b.tx.Exec("DELETE FROM nodes WHERE node_id = ?", nodeID)
	return err
}

func (b *Batch) SetCursor(seq int64) error {
	_, err := b.tx.Exec(setCursorSQL, strconv.FormatInt(seq, 10))
	return err
}

const putNodeSQL = `INSERT INTO nodes(node_id, rel_path, is_dir, version, content_hash, size, local_path)
	 VALUES(?,?,?,?,?,?,?)
	 ON CONFLICT(node_id) DO UPDATE SET
	   rel_path = excluded.rel_path, is_dir = excluded.is_dir, version = excluded.version,
	   content_hash = excluded.content_hash, size = excluded.size, local_path = excluded.local_path`

const setCursorSQL = `INSERT INTO meta(key, value) VALUES('cursor', ?)
	 ON CONFLICT(key) DO UPDATE SET value = excluded.value`

func (i *Index) Put(n Node) error {
	_, err := i.db.Exec(putNodeSQL,
		n.NodeID, n.RelPath, boolToInt(n.IsDir), n.Version, n.ContentHash, n.Size, storedLocalPath(n))
	return err
}

// storedLocalPath keeps the column empty when the node lives under its server name, which is
// almost every node: reads substitute rel_path, so nothing is lost and rows written before
// the column existed behave identically.
func storedLocalPath(n Node) string {
	if n.LocalPath == n.RelPath {
		return ""
	}
	return n.LocalPath
}

func (i *Index) Delete(nodeID string) error {
	_, err := i.db.Exec("DELETE FROM nodes WHERE node_id = ?", nodeID)
	return err
}

// NodeIDByLocalPath returns the node occupying a path on disk. Used when placing a node whose
// server name had to be changed to fit the filesystem, to notice that the name it would take
// is already someone else's.
func (i *Index) NodeIDByLocalPath(localPath string) (string, bool, error) {
	var id string
	err := i.db.QueryRow(
		"SELECT node_id FROM nodes WHERE IIF(local_path = '', rel_path, local_path) = ?", localPath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// All returns all known nodes (for diffing against disk state).
func (i *Index) All() ([]Node, error) { return i.nodesFrom("nodes") }

// SetSeq records the feed seq at which a pull last applied nodeID. Put leaves it as it is.
func (i *Index) SetSeq(nodeID string, seq int64) error {
	_, err := i.db.Exec("UPDATE nodes SET seq = ? WHERE node_id = ?", seq, nodeID)
	return err
}

// Seqs returns the seq recorded by SetSeq for every node, by node id.
func (i *Index) Seqs() (map[string]int64, error) { return i.seqs("") }

// DirSeqs returns the seq recorded by SetSeq for every folder, by node id. Of two folders
// indexed at one server path (a ghost and the live folder that took the path since), the
// one with the higher seq is the live one.
func (i *Index) DirSeqs() (map[string]int64, error) { return i.seqs(" WHERE is_dir = 1") }

// MaxSeq returns the highest seq recorded by SetSeq, 0 when there is none.
func (i *Index) MaxSeq() (int64, error) {
	var m int64
	err := i.db.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM nodes").Scan(&m)
	return m, err
}

// seqs reads node_id → seq; where is a constant filter.
func (i *Index) seqs(where string) (map[string]int64, error) {
	rows, err := i.db.Query("SELECT node_id, seq FROM nodes" + where)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, err
		}
		out[id] = seq
	}
	return out, rows.Err()
}

// nodesFrom reads every row of a table with the nodes schema; table is a constant.
func (i *Index) nodesFrom(table string) ([]Node, error) {
	rows, err := i.db.Query("SELECT node_id, rel_path, is_dir, version, content_hash, size, IIF(local_path = '', rel_path, local_path) FROM " + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var isDir int
		var hash sql.NullString
		if err := rows.Scan(&n.NodeID, &n.RelPath, &isDir, &n.Version, &hash, &n.Size, &n.LocalPath); err != nil {
			return nil, err
		}
		n.IsDir = isDir != 0
		n.ContentHash = hash.String
		out = append(out, n)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- browse + local-copy support (mobile Browser facade) ---

// Children returns the direct children of the folder at parentRelPath ("" = root). Direct means
// rel_path is exactly one path segment deeper than parentRelPath.
func (i *Index) Children(parentRelPath string) ([]Node, error) {
	var rows *sql.Rows
	var err error
	if parentRelPath == "" {
		rows, err = i.db.Query(`SELECT node_id, rel_path, is_dir, version, content_hash, size,
			IIF(local_path = '', rel_path, local_path)
			FROM nodes WHERE instr(rel_path, '/') = 0 ORDER BY is_dir DESC, rel_path`)
	} else {
		// A case-sensitive prefix compare, not LIKE (which ignores ASCII case): the server
		// allows "Docs" and "docs" as siblings, and each lists only its own children.
		rows, err = i.db.Query(`SELECT node_id, rel_path, is_dir, version, content_hash, size,
			IIF(local_path = '', rel_path, local_path)
			FROM nodes WHERE substr(rel_path, 1, length(?1) + 1) = ?1 || '/'
			  AND instr(substr(rel_path, length(?1) + 2), '/') = 0
			ORDER BY is_dir DESC, rel_path`, parentRelPath)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var isDir int
		var hash sql.NullString
		if err := rows.Scan(&n.NodeID, &n.RelPath, &isDir, &n.Version, &hash, &n.Size, &n.LocalPath); err != nil {
			return nil, err
		}
		n.IsDir = isDir != 0
		n.ContentHash = hash.String
		out = append(out, n)
	}
	return out, rows.Err()
}

// Subtree returns the node at relPath and every node below it. relPath must not be ""
// (the root is not a node). The match is case-sensitive: the server allows "Docs" and
// "docs" as siblings, and SQLite's LIKE ignores ASCII case, so a prefix compare is used
// instead — forgetting one sibling must never take the other's tree with it.
func (i *Index) Subtree(relPath string) ([]Node, error) {
	rows, err := i.db.Query(`SELECT node_id, rel_path, is_dir, version, content_hash, size,
		IIF(local_path = '', rel_path, local_path)
		FROM nodes WHERE rel_path = ?1 OR substr(rel_path, 1, length(?1) + 1) = ?1 || '/'`, relPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var isDir int
		var hash sql.NullString
		if err := rows.Scan(&n.NodeID, &n.RelPath, &isDir, &n.Version, &hash, &n.Size, &n.LocalPath); err != nil {
			return nil, err
		}
		n.IsDir = isDir != 0
		n.ContentHash = hash.String
		out = append(out, n)
	}
	return out, rows.Err()
}

// SubtreeOf returns what to remove when one node goes: the node and, when it is the only
// node at its path, every node below that path. Paths are not unique — a ghost can still be
// indexed at a path a live node has taken since — and the index keeps no parent ids, so when
// another node shares the path the children cannot be told apart and only the node itself is
// returned. An unknown id returns nothing.
func (i *Index) SubtreeOf(nodeID string) ([]Node, error) {
	n, ok, err := i.Get(nodeID)
	if err != nil || !ok {
		return nil, err
	}
	var others int
	if err := i.db.QueryRow("SELECT COUNT(*) FROM nodes WHERE rel_path = ? AND node_id != ?",
		n.RelPath, nodeID).Scan(&others); err != nil {
		return nil, err
	}
	if others > 0 {
		return []Node{n}, nil
	}
	return i.Subtree(n.RelPath)
}

// LocalPathShared reports whether another node records the same local copy path as nodeID
// (a ghost and a live file of the same name share one). Such a file is not nodeID's to delete.
func (i *Index) LocalPathShared(nodeID string) bool {
	var n int
	err := i.db.QueryRow(`SELECT COUNT(*) FROM local WHERE node_id != ?1
		AND path = (SELECT path FROM local WHERE node_id = ?1)`, nodeID).Scan(&n)
	return err == nil && n > 0
}

// SetLocal records a downloaded copy (state "cached" or "pinned").
func (i *Index) SetLocal(nodeID, state string, version int64, path string) error {
	_, err := i.db.Exec(`INSERT INTO local(node_id,state,version,path) VALUES(?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, version=excluded.version, path=excluded.path`,
		nodeID, state, version, path)
	return err
}

// SetLocalState changes pinning without claiming that cached bytes have a newer version.
func (i *Index) SetLocalState(nodeID, state string) error {
	_, err := i.db.Exec("UPDATE local SET state=? WHERE node_id=?", state, nodeID)
	return err
}

// LocalStatus returns the cache state ("" if none), whether it is stale vs serverVersion, and path.
func (i *Index) LocalStatus(nodeID string, serverVersion int64) (state string, stale bool, path string) {
	var v int64
	if i.db.QueryRow("SELECT state, version, path FROM local WHERE node_id=?", nodeID).Scan(&state, &v, &path) != nil {
		return "", false, ""
	}
	return state, v < serverVersion, path
}

// LocalPathOf returns the cached file path, or "".
func (i *Index) LocalPathOf(nodeID string) string {
	var p string
	if i.db.QueryRow("SELECT path FROM local WHERE node_id=?", nodeID).Scan(&p) != nil {
		return ""
	}
	return p
}

func (i *Index) DeleteLocal(nodeID string) error {
	_, err := i.db.Exec("DELETE FROM local WHERE node_id=?", nodeID)
	return err
}

// RelocateLocal updates the on-disk path recorded for a node's local copy, used when a
// rename/move changes the node's rel_path and the cached file is moved to match.
func (i *Index) RelocateLocal(nodeID, newPath string) error {
	_, err := i.db.Exec("UPDATE local SET path=? WHERE node_id=?", newPath, nodeID)
	return err
}

// ListPinned returns the node IDs marked pinned.
func (i *Index) ListPinned() []string {
	rows, err := i.db.Query("SELECT node_id FROM local WHERE state='pinned'")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
