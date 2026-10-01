package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	c := Config{ServerURL: "https://x", DeviceToken: "kfd_1", SyncDir: "/data/sync"}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatalf("round-trip: %+v != %+v", got, c)
	}
	if db := StateDBPath(p); filepath.Dir(db) != filepath.Dir(p) || filepath.Base(db) != "state.db" {
		t.Fatalf("StateDBPath: %q", db)
	}
}

// Save replaces the file atomically and tightens the mode of a file created earlier with a
// looser one: the config carries the device token.
func TestSaveTightensExistingModeAndReplacesAtomically(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"server_url":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil { // umask-proof
		t.Fatal(err)
	}
	c := Config{ServerURL: "https://x", DeviceToken: "kfd_1", SyncDir: "/data/sync"}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	got, err := Load(p)
	if err != nil || got != c {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, c)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

// The pin is saved under server_pin and left out entirely when there is none, so configs of
// servers with a publicly trusted certificate do not change.
func TestServerPinRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Config{ServerURL: "https://10.0.0.2", DeviceToken: "kfd_1", SyncDir: "/data/sync", ServerPin: "AB:CD"}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"server_pin": "AB:CD"`) {
		t.Fatalf("saved config lacks server_pin: %s", b)
	}
	if got, err := Load(p); err != nil || got != c {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, c)
	}
	c.ServerPin = ""
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "server_pin") {
		t.Fatalf("empty pin written: %s", b)
	}
}
