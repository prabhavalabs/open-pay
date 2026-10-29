// Package storage configures the S3-compatible object store client shared by
// the services that keep uploaded files (MinIO locally, Garage in production).
package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config describes how to reach one bucket on an S3-compatible store.
type Config struct {
	// Endpoint is host[:port], without a scheme.
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	// Region is sent as-is. Empty lets the client discover it, which needs an
	// extra request and fails on stores that only accept their own region.
	Region string
	UseSSL bool
	// PathStyle addresses buckets as https://host/bucket/key instead of
	// https://bucket.host/key.
	PathStyle bool
}

// ConfigFromEnv reads the store settings:
//
//	S3_ENDPOINT          host:port or URL; an http(s):// scheme sets UseSSL
//	S3_REGION            e.g. "garage"; empty means discover
//	S3_ACCESS_KEY_ID     access key
//	S3_SECRET_ACCESS_KEY secret key
//	S3_BUCKET            bucket name (default kyc-documents)
//	S3_USE_SSL           true/false, overrides the endpoint scheme
//	S3_FORCE_PATH_STYLE  true/false (default true)
//
// The older MINIO_ENDPOINT, MINIO_ACCESS_KEY, MINIO_SECRET_KEY, MINIO_BUCKET
// and MINIO_USE_SSL names are still read when the S3_* name is unset. With
// nothing set the defaults point at a local MinIO.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		AccessKey: firstEnv("minioadmin", "S3_ACCESS_KEY_ID", "MINIO_ACCESS_KEY"),
		SecretKey: firstEnv("minioadmin123", "S3_SECRET_ACCESS_KEY", "MINIO_SECRET_KEY"),
		Bucket:    firstEnv("kyc-documents", "S3_BUCKET", "MINIO_BUCKET"),
		Region:    os.Getenv("S3_REGION"),
		PathStyle: true,
	}

	endpoint, secure, err := parseEndpoint(firstEnv("localhost:9000", "S3_ENDPOINT", "MINIO_ENDPOINT"))
	if err != nil {
		return Config{}, err
	}
	cfg.Endpoint = endpoint
	cfg.UseSSL = secure

	if v := firstEnv("", "S3_USE_SSL", "MINIO_USE_SSL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("S3_USE_SSL: %w", err)
		}
		cfg.UseSSL = b
	}
	if v := os.Getenv("S3_FORCE_PATH_STYLE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("S3_FORCE_PATH_STYLE: %w", err)
		}
		cfg.PathStyle = b
	}
	return cfg, nil
}

// NewClient creates a client for cfg.
func NewClient(cfg Config) (*minio.Client, error) {
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}
	return client, nil
}

// EnsureBucket creates cfg.Bucket when it does not exist. The bucket is
// created private; nothing here grants anonymous access.
func EnsureBucket(ctx context.Context, client *minio.Client, cfg Config) error {
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", cfg.Bucket, err)
	}
	if exists {
		return nil
	}
	if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
		return fmt.Errorf("creating bucket %q: %w", cfg.Bucket, err)
	}
	return nil
}

// parseEndpoint accepts "host:port" or "http(s)://host:port" and returns the
// host part plus whether the scheme asked for TLS.
func parseEndpoint(raw string) (string, bool, error) {
	if !strings.Contains(raw, "://") {
		return strings.TrimSuffix(raw, "/"), false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("S3_ENDPOINT: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return "", false, fmt.Errorf("S3_ENDPOINT: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", false, fmt.Errorf("S3_ENDPOINT: want scheme://host[:port], got %q", raw)
	}
	return u.Host, u.Scheme == "https", nil
}

func firstEnv(fallback string, keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return fallback
}
