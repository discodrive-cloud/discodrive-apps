package index

import (
	"path/filepath"
	"testing"
)

func TestResetDropsPendingNames(t *testing.T) {
	for _, reset := range []string{"clear", "snapshot", "pairing"} {
		t.Run(reset, func(t *testing.T) {
			idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer idx.Close()
			if err := idx.SetPendingNames([]PendingName{{LocalPath: "new/report？", RelPath: "new/report?", SourcePath: "old/report?"}}); err != nil {
				t.Fatal(err)
			}
			switch reset {
			case "clear":
				err = idx.Clear()
			case "snapshot":
				err = idx.ClearKeepingSnapshot()
			case "pairing":
				err = idx.BindMirrorPairing("new-account")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := idx.PendingNames(); err != nil || len(got) != 0 {
				t.Fatalf("stale plan after reset: %+v %v", got, err)
			}
		})
	}
}

func TestRecordPushRollsBackIfPlanCompletionFails(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.SetPendingNames([]PendingName{{LocalPath: "new/report？", RelPath: "new/report?", SourcePath: "old/report?"}}); err != nil {
		t.Fatal(err)
	}
	// Simulate a database write failure precisely at plan completion. The node
	// must not commit independently, or a restart could reuse a completed plan.
	if _, err := idx.db.Exec(`CREATE TRIGGER fail_plan BEFORE UPDATE ON meta WHEN OLD.key = 'pending_names' BEGIN SELECT RAISE(ABORT, 'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	n := Node{NodeID: "new", LocalPath: "new/report？", RelPath: "new/report?", Version: 1}
	if err := idx.RecordPush(n, 42); err == nil {
		t.Fatal("expected transaction failure")
	}
	if _, ok, err := idx.Get(n.NodeID); err != nil || ok {
		t.Fatalf("node committed without plan completion: %v %v", ok, err)
	}
	if names, err := idx.PendingNames(); err != nil || len(names) != 1 {
		t.Fatalf("lost retry plan: %+v %v", names, err)
	}
	if _, err := idx.db.Exec("DROP TRIGGER fail_plan"); err != nil {
		t.Fatal(err)
	}
	if err := idx.RecordPush(n, 42); err != nil {
		t.Fatal(err)
	}
	if names, err := idx.PendingNames(); err != nil || len(names) != 0 {
		t.Fatalf("plan was not completed: %+v %v", names, err)
	}
	seqs, err := idx.Seqs()
	if err != nil || seqs[n.NodeID] != 42 {
		t.Fatalf("wrong node ordering: %v %v", seqs, err)
	}
}
