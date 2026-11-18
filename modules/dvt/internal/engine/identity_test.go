package engine

import (
	"strings"
	"testing"
)

// Verifies: DVT-SRS-082
func TestImplementationIdentification(t *testing.T) {
	cases := map[string][2]string{
		"0.1.0":         {"1.3.6.1.4.1.67686.1.3.1.0.1", "LIBREDVT_0_1"},
		"v0.2.0-beta.1": {"1.3.6.1.4.1.67686.1.3.1.0.2", "LIBREDVT_0_2"},
		"1.12.3-4-gabc": {"1.3.6.1.4.1.67686.1.3.1.1.12", "LIBREDVT_1_12"},
		"999.999":       {"1.3.6.1.4.1.67686.1.3.1.999.999", "LIBREDVT_999_999"},
		"dev":           {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
		"ci":            {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
		"":              {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
		"1000.0":        {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
		"01.1":          {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
		"0.1 x":         {"1.3.6.1.4.1.67686.1.3.1.0.0", "LIBREDVT_0_0"},
	}
	for v, want := range cases {
		uid, name := Implementation(v)
		if uid != want[0] || name != want[1] {
			t.Errorf("Implementation(%q) = %q, %q; want %q, %q", v, uid, name, want[0], want[1])
		}
		if len(uid) > 64 || len(name) > 16 {
			t.Errorf("%q: UID %d chars, name %d chars", v, len(uid), len(name))
		}
	}
	// Every network command passes the identification to the worker.
	c := Config{Version: "0.1.0"}
	p := Peer{Host: "h", Port: 104, CalledAET: "A", CallingAET: "B"}
	for _, cmd := range []string{"echo", "reject", "store", "find", "move"} {
		args := strings.Join(c.peerArgs(p, cmd), " ")
		if !strings.HasPrefix(args, cmd+" ") ||
			strings.Count(args, " --impl-uid 1.3.6.1.4.1.67686.1.3.1.0.1 --impl-version LIBREDVT_0_1") != 1 {
			t.Errorf("%s: worker arguments lack LibreDVT's identification: %s", cmd, args)
		}
	}
}
