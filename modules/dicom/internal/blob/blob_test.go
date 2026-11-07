package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// stores returns every backend to hold to the same contract: the filesystem
// always, and S3 when LIBRE_DCM_TEST_S3_ENDPOINT is set (MinIO in a container is
// enough; see `make test-s3`).
func stores(t *testing.T) map[string]Store {
	t.Helper()
	out := map[string]Store{}
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out["fs"] = fs
	if ep := os.Getenv("LIBRE_DCM_TEST_S3_ENDPOINT"); ep != "" {
		s3, err := NewS3(context.Background(), S3Config{
			Endpoint: ep, Bucket: "libredcm-test", Insecure: true, CreateBucket: true,
			AccessKey: os.Getenv("LIBRE_DCM_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("LIBRE_DCM_TEST_S3_SECRET_KEY"),
			Prefix: fmt.Sprintf("t%d/", time.Now().UnixNano()),
		})
		if err != nil {
			t.Fatal(err)
		}
		out["s3"] = s3
	}
	return out
}

func TestStoreContract(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := s.Check(ctx); err != nil {
				t.Fatal(err)
			}
			key := "1.2.3/1.2.3.4/1.2.3.4.5.dcm"
			body := bytes.Repeat([]byte("DICM"), 1000)
			n, err := s.Put(ctx, key, bytes.NewReader(body))
			if err != nil || n != int64(len(body)) {
				t.Fatalf("put: n=%d err=%v", n, err)
			}
			// Unknown-length reader (streaming upload path).
			if n, err := s.Put(ctx, "a/b/stream.dcm", io.MultiReader(strings.NewReader("abc"), strings.NewReader("def"))); err != nil || n != 6 {
				t.Fatalf("streaming put: n=%d err=%v", n, err)
			}
			rc, err := s.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !bytes.Equal(got, body) {
				t.Fatal("content differs after round trip")
			}
			// Overwrite replaces.
			if _, err := s.Put(ctx, key, bytes.NewReader([]byte("v2"))); err != nil {
				t.Fatal(err)
			}
			rc, _ = s.Get(ctx, key)
			got, _ = io.ReadAll(rc)
			_ = rc.Close()
			if string(got) != "v2" {
				t.Fatalf("overwrite: %q", got)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
				t.Fatalf("get after delete: %v, want ErrNotFound", err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("deleting a missing key must succeed: %v", err)
			}
			for _, bad := range []string{"", "../etc/passwd", "a/../../b"} {
				if _, err := s.Put(ctx, bad, strings.NewReader("x")); err == nil {
					t.Errorf("invalid key %q accepted", bad)
				}
			}
		})
	}
}
