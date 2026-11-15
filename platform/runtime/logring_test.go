package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func ringLogger(level slog.Level, logs, audits *LogBuffer) *slog.Logger {
	return slog.New(newRingHandler(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level}), logs, audits))
}

func field(t *testing.T, e LogEntry, key string) any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(e.Line, &m); err != nil {
		t.Fatalf("ring line is not JSON: %v: %s", err, e.Line)
	}
	return m[key]
}

// The rings have a fixed size: old records are dropped, the newest are kept
// in order, and Select never returns more than its limit.
// Verifies: DCM-SRS-084, DCM-SRS-085
func TestLogBufferIsBounded(t *testing.T) {
	logs, audits := NewLogBuffer(5), NewLogBuffer(3)
	log := ringLogger(slog.LevelInfo, logs, audits)
	for i := 0; i < 12; i++ {
		log.Info("line", "n", i)
		log.Info("audit", "audit", true, "event", "Query", "outcome", "success", "n", i)
	}
	if logs.Len() != 5 || audits.Len() != 3 || logs.Size() != 5 || audits.Size() != 3 {
		t.Fatalf("ring grew: logs %d/%d audits %d/%d", logs.Len(), logs.Size(), audits.Len(), audits.Size())
	}
	got, more := logs.Select(100, nil)
	if len(got) != 5 || more {
		t.Fatalf("select all = %d more=%v", len(got), more)
	}
	for i, e := range got { // oldest first: 7..11
		if n := field(t, e, "n"); n != float64(7+i) {
			t.Fatalf("entry %d holds n=%v, want %d (oldest dropped, order kept)", i, n, 7+i)
		}
	}
	got, more = logs.Select(2, nil)
	if len(got) != 2 || !more || field(t, got[1], "n") != float64(11) || field(t, got[0], "n") != float64(10) {
		t.Fatalf("limited select = %d more=%v", len(got), more)
	}
	if got, more := logs.Select(0, nil); got != nil || more {
		t.Fatal("limit 0 returned records")
	}
	got, _ = audits.Select(100, func(e *LogEntry) bool { return e.Event == "Query" && e.Outcome == "success" })
	if len(got) != 3 || field(t, got[2], "n") != float64(11) {
		t.Fatalf("audit ring = %d", len(got))
	}
}

// Audit records go to the audit ring only, other lines to the log ring only;
// a line below the configured level is in neither; logger attributes set
// with With are kept; the ring adds no field of its own.
// Verifies: DCM-SRS-084, DCM-SRS-085
func TestRingRoutingAndContent(t *testing.T) {
	logs, audits := NewLogBuffer(10), NewLogBuffer(10)
	log := ringLogger(slog.LevelInfo, logs, audits).With("module", "LibreDCM", "node", "n1")
	log.Debug("hidden")
	log.Warn("disk nearly full", "free_mb", 12)
	log.With("component", "dimse").Info("audit", "audit", true, "event", "Query", "outcome", "success", "calling_ae", "CT1")

	l, _ := logs.Select(10, nil)
	a, _ := audits.Select(10, nil)
	if len(l) != 1 || len(a) != 1 {
		t.Fatalf("logs=%d audits=%d, want 1 and 1", len(l), len(a))
	}
	if l[0].Level != slog.LevelWarn || field(t, l[0], "msg") != "disk nearly full" || field(t, l[0], "node") != "n1" {
		t.Fatalf("log entry = %s", l[0].Line)
	}
	if a[0].Event != "Query" || a[0].Outcome != "success" || field(t, a[0], "component") != "dimse" {
		t.Fatalf("audit entry = %+v %s", a[0], a[0].Line)
	}
	m := map[string]any{}
	_ = json.Unmarshal(a[0].Line, &m)
	want := map[string]bool{"time": true, "level": true, "msg": true, "module": true, "node": true, "component": true,
		"audit": true, "event": true, "outcome": true, "calling_ae": true}
	for k := range m {
		if !want[k] {
			t.Errorf("ring added field %q that the logger did not write", k)
		}
	}
	if len(m) != len(want) {
		t.Errorf("ring line has %d fields, want %d: %s", len(m), len(want), a[0].Line)
	}
	// "audit" must be the boolean true: a text value is an ordinary line.
	log.Info("not audit", "audit", "true")
	if audits.Len() != 1 || logs.Len() != 2 {
		t.Fatalf("audit=\"true\" (text) was filed as an audit record")
	}
}

// One very long line cannot make the ring large: it is replaced by a marker.
// Verifies: DCM-SRS-085
func TestRingCapsLineLength(t *testing.T) {
	logs := NewLogBuffer(2)
	log := ringLogger(slog.LevelInfo, logs, NewLogBuffer(1))
	log.Info("big", "blob", strings.Repeat("x", 3*maxRingLine))
	got, _ := logs.Select(1, nil)
	if len(got) != 1 || len(got[0].Line) > maxRingLine || strings.Contains(string(got[0].Line), "xxxx") {
		t.Fatalf("long line kept: %d bytes", len(got[0].Line))
	}
	if field(t, got[0], "bytes") == nil {
		t.Fatalf("marker does not say how long the line was: %s", got[0].Line)
	}
}

// Many goroutines log while others read: no race, no lost bound.
// Verifies: DCM-SRS-084, DCM-SRS-085
func TestRingConcurrentUse(t *testing.T) {
	logs, audits := NewLogBuffer(50), NewLogBuffer(50)
	log := ringLogger(slog.LevelInfo, logs, audits)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				log.Info(fmt.Sprintf("g%d", g), "i", i)
				log.Info("audit", "audit", true, "event", "Query", "outcome", "success")
				if i%20 == 0 {
					for _, e := range func() []LogEntry { out, _ := logs.Select(10, nil); return out }() {
						if !json.Valid(e.Line) {
							t.Errorf("invalid line under load: %s", e.Line)
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if logs.Len() != 50 || audits.Len() != 50 {
		t.Fatalf("after load: logs=%d audits=%d, want 50 and 50", logs.Len(), audits.Len())
	}
}

// The runtime's own logger feeds the rings, and the helpers used by the
// control API report the node's facts.
// Verifies: DCM-SRS-081, DCM-SRS-085
func TestRuntimeFeedsRings(t *testing.T) {
	cfg := DefaultAdmin()
	cfg.LogLevel, cfg.Token, cfg.NodeID = "error", "tok-12345", "node-a"
	r := New("test", "1.0", cfg)
	r.Logger.Info("below level")
	r.Logger.Error("boom", "err", "x")
	got, _ := r.Logs.Select(10, nil)
	if len(got) != 1 || field(t, got[0], "node") != "node-a" || field(t, got[0], "msg") != "boom" {
		t.Fatalf("runtime ring = %d entries", len(got))
	}
	if r.Logs.Size() != LogRingSize || r.Audit.Size() != AuditRingSize {
		t.Fatalf("ring sizes %d/%d", r.Logs.Size(), r.Audit.Size())
	}
	if s := r.AdminSecrets(); len(s) != 1 || s[0] != "tok-12345" {
		t.Fatalf("AdminSecrets = %v", s)
	}
	if r.AdminTLS() || r.Started().IsZero() {
		t.Fatal("AdminTLS/Started wrong")
	}
}
