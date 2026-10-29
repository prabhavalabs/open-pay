package database

import (
	"os"
	"testing"
)

func clearDBEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_SSLMODE", "DB_NAME_PREFIX", "DB_POOL_MAX_CONNS"} {
		t.Setenv(k, "")
	}
	// An unset suffix means the default "_db"; t.Setenv restores it afterwards.
	t.Setenv("DB_NAME_SUFFIX", "")
	if err := os.Unsetenv("DB_NAME_SUFFIX"); err != nil {
		t.Fatal(err)
	}
}

func TestURLFromEnvLocalDefaults(t *testing.T) {
	clearDBEnv(t)
	got := URLFromEnv("payment")
	want := "postgres://olp:olp_dev_password@localhost:7020/payment_db?sslmode=disable"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestURLFromEnvSharedServer(t *testing.T) {
	clearDBEnv(t)
	t.Setenv("DB_HOST", "platform-postgres")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_USER", "openpay")
	t.Setenv("DB_PASSWORD", "p@ss:w/rd")
	t.Setenv("DB_NAME_PREFIX", "openpay_")
	t.Setenv("DB_NAME_SUFFIX", "")
	t.Setenv("DB_POOL_MAX_CONNS", "4")
	got := URLFromEnv("merchant")
	want := "postgres://openpay:p%40ss%3Aw%2Frd@platform-postgres:5432/openpay_merchant?pool_max_conns=4&sslmode=disable"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestURLFromEnvPrefersDatabaseURL(t *testing.T) {
	clearDBEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x:y@db:5432/z")
	t.Setenv("DB_HOST", "ignored")
	if got := URLFromEnv("admin"); got != "postgres://x:y@db:5432/z" {
		t.Fatalf("DATABASE_URL not preferred: %q", got)
	}
}
