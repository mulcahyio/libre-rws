package fingerprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatch(t *testing.T) {
	a := Encode(Parts{"m1", "b1", "d1"})
	cases := []struct {
		have string
		want bool
	}{
		{a, true},
		{Encode(Parts{"m1", "b1", "d2"}), true},  // disk swapped
		{Encode(Parts{"m2", "b1", "d1"}), true},  // OS reinstalled
		{Encode(Parts{"m2", "b2", "d1"}), false}, // two parts changed
		{Encode(Parts{"m1", "b1", ""}), true},    // disk unreadable
		{Encode(Parts{"m1", "", ""}), false},     // only one part known
		{Encode(Parts{"x", "y", "z"}), false},
		{"garbage", false},
	}
	for i, c := range cases {
		if got := Match(a, c.have); got != c.want {
			t.Errorf("case %d: Match = %v, want %v", i, got, c.want)
		}
	}
	if Match("", "") {
		t.Error("empty fingerprints matched")
	}
	if !Valid(a) || Valid("fp1-xyz") {
		t.Error("Valid wrong")
	}
}

func TestCollectContainer(t *testing.T) {
	dir := t.TempDir()
	fp1, p1, err := Collect(Options{DataDir: dir, HostID: "host-123"})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Machine != "host-123" || p1.Board == "" {
		t.Fatalf("parts %+v", p1)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "librekey-install-id")); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatal("install id not written 0600")
	}
	fp2, _, _ := Collect(Options{DataDir: dir, HostID: "host-123"})
	if fp1 != fp2 {
		t.Fatal("fingerprint not stable across runs")
	}
	// Same data volume moved to another PC: host id differs, install id same.
	fp3, _, _ := Collect(Options{DataDir: dir, HostID: "other-host"})
	if fp3 == fp1 {
		t.Fatal("host id ignored")
	}
	// New install on the same PC: install id differs.
	fp4, _, _ := Collect(Options{DataDir: t.TempDir(), HostID: "host-123"})
	if fp4 == fp1 {
		t.Fatal("install id ignored")
	}
}
