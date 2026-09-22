package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActivityReportsPullAndFailure(t *testing.T) {
	body := []byte("hello")
	src := &fakeSource{changes: []Change{{Seq: 1, Op: "create", NodeID: "n", RelPath: "note.txt", ContentHash: hashOf(body), Size: 5}}, bodies: map[string][][]byte{"n": {body}}}
	e, _ := newEngine(t, src)
	e.ObserveChanges(func(c Change, phase string, _ time.Duration, _ error) {
		if phase == "begin" {
			a := e.Activity()
			if a.Phase != "pull" || a.Path != c.RelPath {
				t.Errorf("missing current file: %+v", a)
			}
		}
	})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a := e.Activity(); a.Completed != 1 || a.Phase != "" || len(a.Errors) != 0 {
		t.Fatalf("completed: %+v", a)
	}
	src.changes = append(src.changes, Change{Seq: 2, Op: "create", NodeID: "missing", RelPath: "other.txt"})
	src.failOn = "missing"
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatal("expected download failure")
	}
	if a := e.Activity(); len(a.Errors) != 1 || a.Errors[0].Path != "other.txt" || a.Phase != "" || a.Completed != 1 {
		t.Fatalf("failure: %+v", a)
	}
}
func TestActivityBoundedIndependentAndConcurrent(t *testing.T) {
	e := &Engine{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			e.Activity()
		}
	}()
	for i := 0; i < 100; i++ {
		e.activityBegin("push", fmt.Sprint(i))
		e.activityEnd(errors.New("disk full"))
	}
	wg.Wait()
	a := e.Activity()
	if len(a.Errors) != 20 {
		t.Fatalf("unbounded errors: %d", len(a.Errors))
	}
	a.Errors[0].Message = "modified by caller"
	if e.Activity().Errors[0].Message != "disk full" {
		t.Fatal("snapshot shares storage")
	}
	e.activityBegin("push", "99")
	e.activityEnd(errors.New("retry failed"))
	if len(e.Activity().Errors) != 20 {
		t.Fatal("retry duplicated path")
	}
	e.activityBegin("scan", "")
	e.activityEnd(nil)
	e.activityBegin("push", "cancelled")
	e.activityEnd(context.Canceled)
	if a := e.Activity(); a.Completed != 0 || len(a.Errors) != 20 || a.Phase != "" {
		t.Fatalf("scan/cancellation counted: %+v", a)
	}
}

func TestActivityReportsActualPush(t *testing.T) {
	e, root := newEngine(t, nil)
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "note.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatal(err)
	}
	if a := e.Activity(); a.Completed != 2 || a.Path != "" || a.Phase != "" || len(a.Errors) != 0 {
		t.Fatalf("push: %+v", a)
	}
}
