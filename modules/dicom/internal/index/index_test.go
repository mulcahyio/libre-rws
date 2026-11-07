package index

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func rec(n int) Record {
	return Record{
		PatientID: "P1", StudyUID: "1.2.3", SeriesUID: "1.2.3.1",
		SOPInstanceUID: fmt.Sprintf("1.2.3.1.%d", n), SOPClassUID: "1.2.840.10008.5.1.4.1.1.2",
		TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: fmt.Sprintf("k%d", n),
	}
}

// TestBackupWhileWriting takes a snapshot while another goroutine keeps
// inserting, then checks the snapshot opens as a valid index with a
// consistent instance count (issue #1).
func TestBackupWhileWriting(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	idx, err := Open(ctx, "sqlite", "file:"+filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	for i := 0; i < 50; i++ {
		if err := idx.Upsert(ctx, rec(i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 50; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := idx.Upsert(ctx, rec(i)); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	dest := filepath.Join(dir, "backups", "snap.db")
	for range 2 { // second run replaces the first snapshot
		if err := idx.Backup(ctx, dest); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	snap, err := Open(ctx, "sqlite", "file:"+dest)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	rows, err := snap.Find(ctx, Image, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 50 {
		t.Fatalf("snapshot has %d instances, want at least 50", len(rows))
	}
	st, err := snap.Find(ctx, Study, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0]["NumberOfStudyRelatedInstances"] != fmt.Sprint(len(rows)) {
		t.Fatalf("snapshot inconsistent: study row %v vs %d instances", st, len(rows))
	}
}

func TestBackupRejectsEmptyDest(t *testing.T) {
	idx, err := Open(context.Background(), "sqlite", "file:"+filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.Backup(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty destination")
	}
}
