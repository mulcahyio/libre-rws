// Package blob stores DICOM Part 10 files. The interface is deliberately small
// so the filesystem backend (single node, or a shared volume for a cluster) can
// be swapped for object storage (S3 / MinIO) without touching the gateway.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("blob: not found")

// Store is the storage contract used by the DICOM module.
type Store interface {
	// Put writes the object atomically: readers never observe a partial file.
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// Check verifies the backend is usable (for readiness probes).
	Check(ctx context.Context) error
}

// FS stores objects as files under Root.
type FS struct {
	Root string
}

// NewFS creates the root directory if needed.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("blob: create root: %w", err)
	}
	return &FS{Root: root}, nil
}

func (f *FS) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(key, "..") || clean == "/" {
		return "", fmt.Errorf("blob: invalid key %q", key)
	}
	return filepath.Join(f.Root, clean), nil
}

// Put writes to a temp file, fsyncs, then renames into place.
func (f *FS) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	dst, err := f.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".incoming-*")
	if err != nil {
		return 0, err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }

	n, err := io.Copy(tmp, readerWithContext(ctx, r))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		cleanup()
		return 0, err
	}
	return n, nil
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	fh, err := os.Open(p) // #nosec G304 -- p comes from FS.path, which rejects ".." and anchors keys under Root
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return fh, err
}

func (f *FS) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (f *FS) Check(_ context.Context) error {
	tmp, err := os.CreateTemp(f.Root, ".probe-*")
	if err != nil {
		return fmt.Errorf("storage not writable: %w", err)
	}
	name := tmp.Name()
	_ = tmp.Close()
	return os.Remove(name)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func readerWithContext(ctx context.Context, r io.Reader) io.Reader { return ctxReader{ctx, r} }
