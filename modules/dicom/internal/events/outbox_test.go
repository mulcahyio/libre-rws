package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"rws/modules/dicom/internal/index"
)

// flakyPub fails while down is true and records what it published.
type flakyPub struct {
	mu   sync.Mutex
	down bool
	got  []string
}

func (f *flakyPub) Publish(_ context.Context, _, msgID string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("nats: no servers available")
	}
	f.got = append(f.got, msgID)
	return nil
}
func (f *flakyPub) Check(context.Context) error { return nil }
func (f *flakyPub) Close() error                { return nil }
func (f *flakyPub) set(down bool)               { f.mu.Lock(); f.down = down; f.mu.Unlock() }
func (f *flakyPub) published() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

// TestRelaySurvivesBusOutage: events stored while the bus is down are
// published exactly once, after it comes back.
func TestRelaySurvivesBusOutage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idx, err := index.Open(ctx, "sqlite", "file:"+filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	pub := &flakyPub{down: true}
	fails := 0
	var fmu sync.Mutex
	r := NewRelay(idx, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, func() { fmu.Lock(); fails++; fmu.Unlock() })
	r.Interval, r.Lease, r.MaxDelay = 20*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
	go func() { _ = r.Run(ctx) }()

	ids := []string{"e1", "e2", "e3"}
	for _, id := range ids {
		rec := index.Record{StudyUID: "1.2", SeriesUID: "1.2.1", SOPInstanceUID: id, SOPClassUID: "x",
			TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: id}
		if err := idx.UpsertWithEvent(ctx, rec, &index.OutboxEvent{MsgID: id, Subject: SubjectInstanceStored, Payload: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
		r.Nudge()
	}
	time.Sleep(200 * time.Millisecond)
	if len(pub.published()) != 0 {
		t.Fatal("published while bus down")
	}
	if n, _ := idx.PendingEvents(ctx); n != 3 {
		t.Fatalf("pending during outage = %d, want 3", n)
	}

	pub.set(false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := idx.PendingEvents(ctx); n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := pub.published()
	sort.Strings(got)
	if len(got) != 3 || got[0] != "e1" || got[1] != "e2" || got[2] != "e3" {
		t.Fatalf("published after recovery = %v, want %v", got, ids)
	}
	fmu.Lock()
	defer fmu.Unlock()
	if fails == 0 {
		t.Fatal("expected failed attempts to be counted")
	}
}

func TestBackoff(t *testing.T) {
	r := &Relay{MaxDelay: 10 * time.Second}
	for attempts, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 9: 10 * time.Second} {
		if got := r.backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
