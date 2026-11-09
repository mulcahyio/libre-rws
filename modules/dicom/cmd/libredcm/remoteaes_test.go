package main

import "testing"

// Verifies: DCM-SRS-013
func TestParseRemoteAEs(t *testing.T) {
	m, err := parseRemoteAEs(" VIEWER1=host.docker.internal:11113 ; PACS_B=10.0.0.5:104:PACSB,")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["VIEWER1"].Port != 11113 || m["VIEWER1"].Host != "host.docker.internal" ||
		m["PACS_B"].AETitle != "PACSB" || m["PACS_B"].Port != 104 {
		t.Fatalf("unexpected: %+v", m)
	}
	for _, bad := range []string{"X", "X=h", "X=h:0", "X=h:abc", "=h:1", "X=:104", "X=h:1:2:3", "WAY_TOO_LONG_AE_TITLE=h:1"} {
		if _, err := parseRemoteAEs(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
