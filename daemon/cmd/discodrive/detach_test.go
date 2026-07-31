package main

import "testing"

func TestShouldDetach(t *testing.T) {
	cases := []struct {
		name                    string
		detach, foreground, tty bool
		want                    bool
	}{
		{"interactive console run auto-detaches", false, false, true, true},
		{"explicit --foreground keeps the console", false, true, true, false},
		{"service run (no tty) stays foreground", false, false, false, false},
		{"explicit --detach works even without tty", true, false, false, true},
		{"--detach wins over --foreground", true, true, false, true},
	}
	for _, c := range cases {
		if got := shouldDetach(c.detach, c.foreground, c.tty); got != c.want {
			t.Errorf("%s: shouldDetach(%v,%v,%v)=%v want %v", c.name, c.detach, c.foreground, c.tty, got, c.want)
		}
	}
}
