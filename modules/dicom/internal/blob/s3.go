package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures the object-store backend. It works with AWS S3 and any
// S3-compatible store (MinIO, Ceph RGW, NAS appliances with an S3 API).
type S3Config struct {
	Endpoint     string `yaml:"endpoint" env:"RWS_STORAGE_S3_ENDPOINT"` // host[:port], e.g. s3.amazonaws.com or minio:9000
	Bucket       string `yaml:"bucket" env:"RWS_STORAGE_S3_BUCKET"`
	Region       string `yaml:"region" env:"RWS_STORAGE_S3_REGION"`
	Prefix       string `yaml:"prefix" env:"RWS_STORAGE_S3_PREFIX"` // optional key prefix, e.g. "site-a/"
	AccessKey    string `yaml:"access_key" env:"RWS_STORAGE_S3_ACCESS_KEY"`
	SecretKey    string `yaml:"secret_key" env:"RWS_STORAGE_S3_SECRET_KEY"`
	Insecure     bool   `yaml:"insecure" env:"RWS_STORAGE_S3_INSECURE"`           // plain HTTP (lab / same-host MinIO only)
	CreateBucket bool   `yaml:"create_bucket" env:"RWS_STORAGE_S3_CREATE_BUCKET"` // create the bucket at start if missing
}

// S3 stores each object under Prefix+key in one bucket. A PUT is atomic on
// S3 (an object is visible whole or not at all), which satisfies Store.Put.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
}

// NewS3 connects and checks the bucket (creating it when configured).
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blob: s3 needs endpoint and bucket")
	}
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	if cfg.AccessKey == "" { // fall back to the usual AWS environment / instance role chain
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{}, &credentials.EnvMinio{}, &credentials.IAM{},
		})
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: !cfg.Insecure, Region: cfg.Region})
	if err != nil {
		return nil, fmt.Errorf("blob: s3 client: %w", err)
	}
	s := &S3{c: c, bucket: cfg.Bucket, prefix: strings.TrimPrefix(cfg.Prefix, "/")}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ok, err := c.BucketExists(cctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("blob: s3 bucket %q: %w", cfg.Bucket, err)
	}
	if !ok {
		if !cfg.CreateBucket {
			return nil, fmt.Errorf("blob: s3 bucket %q does not exist (set create_bucket to create it)", cfg.Bucket)
		}
		if err := c.MakeBucket(cctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("blob: create s3 bucket %q: %w", cfg.Bucket, err)
		}
	}
	return s, nil
}

func (s *S3) key(k string) (string, error) {
	if k == "" || strings.Contains(k, "..") || strings.HasPrefix(k, "/") {
		return "", fmt.Errorf("blob: invalid key %q", k)
	}
	return path.Join(s.prefix, k), nil
}

// lenReader is implemented by bytes.Reader, strings.Reader and bytes.Buffer.
type lenReader interface{ Len() int }

// Put uploads the object with a Content-MD5 so the store rejects a corrupted
// upload instead of keeping it.
func (s *S3) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	k, err := s.key(key)
	if err != nil {
		return 0, err
	}
	size := int64(-1) // unknown: multipart streaming
	if lr, ok := r.(lenReader); ok {
		size = int64(lr.Len())
	}
	info, err := s.c.PutObject(ctx, s.bucket, k, r, size, minio.PutObjectOptions{
		ContentType: "application/dicom", SendContentMd5: true,
	})
	if err != nil {
		return 0, fmt.Errorf("blob: s3 put %s: %w", k, err)
	}
	return info.Size, nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, err
	}
	obj, err := s.c.GetObject(ctx, s.bucket, k, minio.GetObjectOptions{})
	if err != nil {
		return nil, s3Err(err)
	}
	if _, err := obj.Stat(); err != nil { // GetObject is lazy; Stat surfaces NoSuchKey now
		_ = obj.Close()
		return nil, s3Err(err)
	}
	return obj, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	if err := s.c.RemoveObject(ctx, s.bucket, k, minio.RemoveObjectOptions{}); err != nil {
		if errors.Is(s3Err(err), ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// Check confirms the bucket is reachable with the configured credentials.
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.c.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("s3 unreachable: %w", err)
	}
	if !ok {
		return fmt.Errorf("s3 bucket %q missing", s.bucket)
	}
	return nil
}

func s3Err(err error) error {
	if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
		return ErrNotFound
	}
	return err
}
