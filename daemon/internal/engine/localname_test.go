package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Server paths may contain characters the local filesystem rejects — Android's storage and
// Windows both refuse `? : * " < > | \`. A note called "worth it?.md" downloaded fine and then
// failed to be moved into place with EPERM, which stalled every change behind it.

func TestPullPlacesRejectedNameUnderALocalisedOne(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{{Seq: 1, Op: "create", NodeID: "n1", RelPath: "notes/worth it?.md",
			ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "notes", "worth it？.md"))
	if err != nil || string(got) != "note" {
		t.Fatalf("localised file: %q err=%v", got, err)
	}
	n, ok, err := e.idx.Get("n1")
	if err != nil || !ok {
		t.Fatalf("index: ok=%v err=%v", ok, err)
	}
	if n.RelPath != "notes/worth it?.md" {
		t.Errorf("RelPath = %q, the server name must be kept as-is", n.RelPath)
	}
	if n.LocalPath != "notes/worth it？.md" {
		t.Errorf("LocalPath = %q", n.LocalPath)
	}
}

// The push side matches disk against the index. If it did not know the file sits under a
// different name, it would read the localised file as a new one and the server name as
// deleted — uploading a duplicate and removing the original.
func TestDetectLocalSeesNoChangeAfterLocalisedPull(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		// The folder arrives as its own feed entry, as the server sends it — otherwise
		// the directory created implicitly for the file reads as a local addition.
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "d1", RelPath: "notes", IsDir: true},
			{Seq: 2, Op: "create", NodeID: "n1", RelPath: "notes/worth it?.md",
				ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, _ := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	changes, err := e.DetectLocal()
	if err != nil {
		t.Fatalf("DetectLocal: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no local changes, got %+v", changes)
	}
}

// Sweeping orphans walks the disk and keeps what the index knows. It has to compare against
// local names, or it would delete every localised file right after downloading it.
func TestSweepOrphansKeepsLocalisedNames(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{{Seq: 1, Op: "create", NodeID: "n1", RelPath: "notes/worth it?.md",
			ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if err := e.sweepOrphans(); err != nil {
		t.Fatalf("sweepOrphans: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "worth it？.md")); err != nil {
		t.Fatalf("localised file was swept away: %v", err)
	}
}

// A rename on the server has to move the file that is actually on disk, under its local name.
func TestRenameBetweenLocalisedNames(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "n1", RelPath: "a?.md", ContentHash: hashOf(body), Size: int64(len(body))},
			{Seq: 2, Op: "rename", NodeID: "n1", RelPath: "b?.md", ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b？.md")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a？.md")); err == nil {
		t.Error("old name still on disk")
	}
}
