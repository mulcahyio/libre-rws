package index

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// openTestIndexes returns a fresh SQLite index and, when
// LIBRE_DCM_TEST_POSTGRES_DSN is set, a PostgreSQL index with empty tables, so the
// same tests hold both engines to one behaviour.
func openTestIndexes(t *testing.T) map[string]*SQL {
	t.Helper()
	ctx := context.Background()
	out := map[string]*SQL{}
	lite, err := Open(ctx, "sqlite", "file:"+filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lite.Close() })
	out["sqlite"] = lite
	if dsn := os.Getenv("LIBRE_DCM_TEST_POSTGRES_DSN"); dsn != "" {
		pg, err := Open(ctx, "postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"event_outbox", "instances", "series", "studies", "conflict_instances", "conflicts"} {
			if _, err := pg.db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = pg.Close() })
		out["postgres"] = pg
	}
	return out
}

func ev(id string) *OutboxEvent {
	return &OutboxEvent{MsgID: id, Subject: "libre.dcm.instance.stored", Payload: []byte(`{"id":"` + id + `"}`)}
}

// Verifies: DCM-SRS-030
func TestOutboxLifecycle(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for i, id := range []string{"a", "b", "c"} {
				if err := idx.UpsertWithEvent(ctx, rec(i), ev(id)); err != nil {
					t.Fatal(err)
				}
			}
			// A resent instance with the same event id does not duplicate the event.
			if err := idx.UpsertWithEvent(ctx, rec(0), ev("a")); err != nil {
				t.Fatal(err)
			}
			if n, _ := idx.PendingEvents(ctx); n != 3 {
				t.Fatalf("pending = %d, want 3", n)
			}

			got, err := idx.ClaimEvents(ctx, 2, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].MsgID != "a" || got[1].MsgID != "b" || got[0].Attempts != 1 {
				t.Fatalf("first claim = %+v", got)
			}
			if string(got[0].Payload) != `{"id":"a"}` {
				t.Fatalf("payload = %s", got[0].Payload)
			}
			// Leased events are not handed out again.
			got2, _ := idx.ClaimEvents(ctx, 10, time.Minute)
			if len(got2) != 1 || got2[0].MsgID != "c" {
				t.Fatalf("second claim = %+v", got2)
			}

			if err := idx.AckEvent(ctx, "a"); err != nil {
				t.Fatal(err)
			}
			// A nacked event becomes due again at retryAt.
			if err := idx.NackEvent(ctx, "b", time.Now().Add(-time.Second), "bus down"); err != nil {
				t.Fatal(err)
			}
			got3, _ := idx.ClaimEvents(ctx, 10, time.Minute)
			if len(got3) != 1 || got3[0].MsgID != "b" || got3[0].Attempts != 2 {
				t.Fatalf("claim after nack = %+v", got3)
			}
			if n, _ := idx.PendingEvents(ctx); n != 2 {
				t.Fatalf("pending after ack = %d, want 2", n)
			}
		})
	}
}

// TestOutboxConcurrentClaims checks that parallel claimers (several nodes on
// one shared index) never receive the same event twice.
// Verifies: DCM-SRS-030
func TestOutboxConcurrentClaims(t *testing.T) {
	for name, idx := range openTestIndexes(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const total = 200
			for i := range total {
				if err := idx.UpsertWithEvent(ctx, rec(i), ev(rec(i).SOPInstanceUID)); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			seen := map[string]int{}
			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						evs, err := idx.ClaimEvents(ctx, 7, time.Minute)
						if err != nil {
							t.Error(err)
							return
						}
						if len(evs) == 0 {
							return
						}
						mu.Lock()
						for _, e := range evs {
							seen[e.MsgID]++
						}
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if len(seen) != total {
				t.Fatalf("claimed %d distinct events, want %d", len(seen), total)
			}
			for id, n := range seen {
				if n != 1 {
					t.Fatalf("event %s claimed %d times", id, n)
				}
			}
		})
	}
}
