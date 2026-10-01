package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The server refuses (409) to purge a trashed folder that still holds a live item. The app
// gets that as TrashBlockedMarker, to explain it in its own words; other failures pass as
// they are.
func TestPurgeRefusedForALiveItemCarriesTheMarker(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	conflict := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "folder holds items that are not in the trash"})
	}
	mux.HandleFunc("DELETE /files/{id}/purge", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "boom" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conflict(w, r)
	})
	mux.HandleFunc("DELETE /files/trash", conflict)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b, err := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	for name, err := range map[string]error{"purge": b.Purge("t1"), "empty": b.EmptyTrash()} {
		if err == nil || !strings.HasPrefix(err.Error(), TrashBlockedMarker) {
			t.Errorf("%s: %v, want it to start with %q", name, err, TrashBlockedMarker)
		}
	}
	if err := b.Purge("boom"); err == nil || strings.Contains(err.Error(), TrashBlockedMarker) {
		t.Errorf("a 500 must pass as it is, got %v", err)
	}
}
