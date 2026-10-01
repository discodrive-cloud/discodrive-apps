package main

import "testing"

func TestWithWarning(t *testing.T) {
	if got := withWarning("Status: synced", "", 0); got != "Status: synced" {
		t.Fatalf("no error: %q", got)
	}
	if got := withWarning("Status: synced", "big.bin: 413 too large", 0); got != "Status: synced — big.bin: 413 too large" {
		t.Fatalf("with error: %q", got)
	}
	if got := withWarning("S", "абвгд", 3); got != "S — абв…" {
		t.Fatalf("shortened: %q", got)
	}
}
