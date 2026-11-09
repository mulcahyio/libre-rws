package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestUpgradeFromV1 opens an index created by 0.1.x (schema v1: no outbox,
// no folded name column) and checks it is upgraded in place.
// Verifies: DCM-SRS-021
func TestUpgradeFromV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The v1 schema is the first ten base statements (v2 added the outbox).
	for _, m := range migrations[:10] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO studies (study_uid, patient_name, updated_at) VALUES ('1.2.3', 'Ünal^Ayşe', 'x')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for range 2 { // a second open must be a no-op
		idx, err := Open(ctx, "sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		var fold string
		if err := idx.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if err := idx.db.QueryRow(`SELECT patient_name_fold FROM studies WHERE study_uid='1.2.3'`).Scan(&fold); err != nil {
			t.Fatal(err)
		}
		if v != schemaVersion || fold != "ünal^ayşe" {
			t.Fatalf("version %d fold %q", v, fold)
		}
		if _, err := idx.PendingEvents(ctx); err != nil {
			t.Fatalf("outbox missing after upgrade: %v", err)
		}
		_ = idx.Close()
	}
}
