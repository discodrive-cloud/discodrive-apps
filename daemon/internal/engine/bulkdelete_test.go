package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A sync folder that lost its contents — a reinstall that kept the index, an unmounted disk, a
// folder moved by hand — looks exactly like "the user deleted everything". Pushing that wipes
// the server, which is what happened: a phone re-paired onto a stale index and deleted the
// whole vault. Past a threshold the push refuses and says so.

func TestPushRefusesToDeleteMostOfTheIndex(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	// Ten files, known to the index and present on disk.
	for i := range 10 {
		name := filepath.Join(root, string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatalf("seeding push: %v", err)
	}

	// The folder is wiped, as a lost mirror would be.
	for i := range 10 {
		if err := os.Remove(filepath.Join(root, string(rune('a'+i))+".txt")); err != nil {
			t.Fatal(err)
		}
	}

	sink := newFakeSink()
	err := e.PushLocal(context.Background(), sink)
	var bulk *BulkDeleteError
	if !errors.As(err, &bulk) {
		t.Fatalf("err = %v, want a BulkDeleteError", err)
	}
	if bulk.Deletions != 10 || bulk.Known != 10 {
		t.Errorf("reported %d of %d, want 10 of 10", bulk.Deletions, bulk.Known)
	}
	if len(sink.deleted) != 0 {
		t.Errorf("%d deletions were sent; the push must send nothing", len(sink.deleted))
	}
}

// Under the threshold everyday deletions go through untouched.
func TestPushAllowsOrdinaryDeletions(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	for i := range 20 {
		if err := os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatalf("seeding push: %v", err)
	}

	// Two of twenty — 10%, below the threshold.
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.Remove(filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if len(sink.deleted) != 2 {
		t.Errorf("sent %d deletions, want 2", len(sink.deleted))
	}
}

// Small folders must stay usable: deleting two files out of three is over the percentage but
// is plainly deliberate, and blocking it would be the safety check making itself a nuisance.
func TestPushAllowsClearingATinyFolder(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatalf("seeding push: %v", err)
	}
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.Remove(filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Errorf("sent %d deletions, want 3", len(sink.deleted))
	}
}

// The user can mean it. Once told so, the very next push goes through, and only that one.
func TestConfirmedBulkDeleteGoesThroughOnce(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	for i := range 10 {
		if err := os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatalf("seeding push: %v", err)
	}
	for i := range 10 {
		if err := os.Remove(filepath.Join(root, string(rune('a'+i))+".txt")); err != nil {
			t.Fatal(err)
		}
	}

	e.ConfirmBulkDelete()
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("confirmed push: %v", err)
	}
	if len(sink.deleted) != 10 {
		t.Errorf("sent %d deletions, want 10", len(sink.deleted))
	}
	if e.bulkDeleteConfirmed {
		t.Error("the confirmation must apply to one push only")
	}
}
