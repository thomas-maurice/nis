// Package s3backup is a thin wrapper around minio-go for the P12 scheduled
// backup feature. Owns the S3 client lifecycle, the HeadBucket fail-fast
// check at startup, and the Put/Get/Delete/List operations the BackupService
// needs. Nothing more — see internal/application/services/backup_service.go
// for the orchestration.
package s3backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config holds the resolved S3 configuration. Built from viper keys
// backups.s3.* by serve.go and passed once at construction.
type Config struct {
	Endpoint        string // e.g. "minio:9000" or "s3.amazonaws.com"
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool // MinIO/Garage need path style; AWS uses virtual-hosted
	UseSSL          bool // true = https, false = plain http (dev MinIO)
	ObjectPrefix    string
}

// Client wraps minio.Client with the operations BackupService uses. Methods
// are safe for concurrent use (minio-go is goroutine-safe).
type Client struct {
	mc     *minio.Client
	bucket string
	prefix string
}

// ErrBucketMissing is returned by New if HeadBucket reports the bucket
// doesn't exist. We never CreateBucket — auto-create would silently mask
// a misconfigured bucket name and create a fresh, unlifecycled, unencrypted
// bucket somewhere.
var ErrBucketMissing = errors.New("s3 backup bucket does not exist")

// New constructs the client and verifies the bucket is reachable. Returns
// ErrBucketMissing if the bucket is absent on the endpoint.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("s3backup: endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3backup: bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3backup: access key + secret key are required")
	}

	endpoint := strings.TrimPrefix(cfg.Endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookup(cfg.UsePathStyle),
	})
	if err != nil {
		return nil, fmt.Errorf("s3backup: construct client: %w", err)
	}

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	exists, err := mc.BucketExists(hctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3backup: BucketExists(%q): %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w: %q on %q", ErrBucketMissing, cfg.Bucket, endpoint)
	}

	return &Client{mc: mc, bucket: cfg.Bucket, prefix: strings.Trim(cfg.ObjectPrefix, "/")}, nil
}

func bucketLookup(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupDNS
}

// ObjectKey returns the in-bucket key for a backup. Operators are scoped
// by their UUID; timestamp encodes the take time. The shape is stable so
// out-of-band tools (lifecycle policies, listing) can predict it.
//
// Example: prefix="nis", operator="abc", ts="2026-05-20T14:00:00Z" →
// "nis/abc/2026-05-20T14:00:00Z.yaml"
func (c *Client) ObjectKey(operatorID, takenAtRFC3339 string) string {
	parts := make([]string, 0, 3)
	if c.prefix != "" {
		parts = append(parts, c.prefix)
	}
	parts = append(parts, operatorID, takenAtRFC3339+".yaml")
	return strings.Join(parts, "/")
}

// Put uploads payload to objectKey. ContentType is application/x-yaml.
// Returns the server-confirmed size.
func (c *Client) Put(ctx context.Context, objectKey string, body io.Reader, size int64) (int64, error) {
	info, err := c.mc.PutObject(ctx, c.bucket, objectKey, body, size, minio.PutObjectOptions{
		ContentType: "application/x-yaml",
	})
	if err != nil {
		return 0, fmt.Errorf("s3backup: PutObject(%q): %w", objectKey, err)
	}
	return info.Size, nil
}

// Get fetches a backup body. Caller must Close the reader. Returns a
// shielded error if the object is missing — callers can distinguish via
// errors.Is(err, ErrObjectMissing).
func (c *Client) Get(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3backup: GetObject(%q): %w", objectKey, err)
	}
	// minio-go returns a non-nil Object even on missing keys; the error
	// surfaces only when the caller reads or calls Stat. Probe Stat now
	// so we can return a typed error early.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isNoSuchKey(err) {
			return nil, fmt.Errorf("%w: %s", ErrObjectMissing, objectKey)
		}
		return nil, fmt.Errorf("s3backup: Stat(%q): %w", objectKey, err)
	}
	return obj, nil
}

// ErrObjectMissing is returned by Get when the object doesn't exist.
var ErrObjectMissing = errors.New("s3 backup object does not exist")

// Remove deletes one object. Idempotent — succeeds silently on missing keys
// (S3 DeleteObject semantics).
func (c *Client) Remove(ctx context.Context, objectKey string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("s3backup: RemoveObject(%q): %w", objectKey, err)
	}
	return nil
}

func isNoSuchKey(err error) bool {
	if err == nil {
		return false
	}
	if er, ok := err.(minio.ErrorResponse); ok {
		return er.Code == "NoSuchKey"
	}
	// minio-go sometimes wraps; fall back to string match for robustness.
	return strings.Contains(err.Error(), "NoSuchKey")
}
