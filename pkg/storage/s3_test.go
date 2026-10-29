package storage

import "testing"

var storageEnv = []string{
	"S3_ENDPOINT", "S3_REGION", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY",
	"S3_BUCKET", "S3_USE_SSL", "S3_FORCE_PATH_STYLE",
	"MINIO_ENDPOINT", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY", "MINIO_BUCKET", "MINIO_USE_SSL",
}

func clearStorageEnv(t *testing.T) {
	t.Helper()
	for _, k := range storageEnv {
		t.Setenv(k, "")
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	clearStorageEnv(t)
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Endpoint: "localhost:9000", AccessKey: "minioadmin", SecretKey: "minioadmin123",
		Bucket: "kyc-documents", PathStyle: true,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestConfigFromEnvGarage(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("S3_ENDPOINT", "http://platform-garage:3900")
	t.Setenv("S3_REGION", "garage")
	t.Setenv("S3_ACCESS_KEY_ID", "GKid")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("S3_BUCKET", "openpay-kyc-documents")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Endpoint: "platform-garage:3900", AccessKey: "GKid", SecretKey: "secret",
		Bucket: "openpay-kyc-documents", Region: "garage", PathStyle: true,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestConfigFromEnvLegacyNamesAndOverrides(t *testing.T) {
	clearStorageEnv(t)
	t.Setenv("MINIO_ENDPOINT", "minio:9000")
	t.Setenv("MINIO_ACCESS_KEY", "legacy")
	t.Setenv("MINIO_BUCKET", "old-bucket")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("S3_FORCE_PATH_STYLE", "false")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "s3.example.com" || cfg.UseSSL || cfg.PathStyle {
		t.Fatalf("endpoint/ssl/path-style not applied: %+v", cfg)
	}
	if cfg.AccessKey != "legacy" || cfg.Bucket != "old-bucket" {
		t.Fatalf("legacy names not read: %+v", cfg)
	}
}

func TestConfigFromEnvRejectsBadEndpoint(t *testing.T) {
	for _, ep := range []string{"ftp://host:21", "http://host:3900/bucket"} {
		clearStorageEnv(t)
		t.Setenv("S3_ENDPOINT", ep)
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatalf("expected an error for %q", ep)
		}
	}
}

func TestNewClientUsesPathStyle(t *testing.T) {
	c, err := NewClient(Config{Endpoint: "platform-garage:3900", AccessKey: "a", SecretKey: "b", Region: "garage", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.EndpointURL().Host; got != "platform-garage:3900" {
		t.Fatalf("endpoint host %q", got)
	}
}
