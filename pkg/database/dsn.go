package database

import (
	"net"
	"net/url"
	"os"
)

// Local development defaults. They match docker-compose.yml, which publishes
// Postgres on host port 7020 with one superuser and <service>_db databases.
const (
	defaultHost       = "localhost"
	defaultPort       = "7020"
	defaultUser       = "olp"
	defaultPassword   = "olp_dev_password"
	defaultNameSuffix = "_db"
	defaultSSLMode    = "disable"
)

// URLFromEnv returns the connection URL for one service's database.
//
// DATABASE_URL wins when it is set. Otherwise the URL is built from
// DB_HOST, DB_PORT, DB_USER, DB_PASSWORD and DB_SSLMODE, and the database
// name is DB_NAME_PREFIX + service + DB_NAME_SUFFIX. DB_POOL_MAX_CONNS, when
// set, caps the service's connection pool (pool_max_conns). With nothing set
// this gives the local development database, e.g. payment_db on
// localhost:7020.
// On a shared server set DB_NAME_PREFIX=openpay_ and DB_NAME_SUFFIX= (empty)
// to get openpay_payment.
func URLFromEnv(service string) string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	params := url.Values{"sslmode": {envOr("DB_SSLMODE", defaultSSLMode)}}
	if v := os.Getenv("DB_POOL_MAX_CONNS"); v != "" {
		params.Set("pool_max_conns", v)
	}
	return BuildURL(
		envOr("DB_HOST", defaultHost),
		envOr("DB_PORT", defaultPort),
		envOr("DB_USER", defaultUser),
		envOr("DB_PASSWORD", defaultPassword),
		DatabaseName(service),
		params,
	)
}

// DatabaseName returns DB_NAME_PREFIX + service + DB_NAME_SUFFIX. An unset
// suffix defaults to "_db"; a suffix set to the empty string is kept empty.
func DatabaseName(service string) string {
	suffix, ok := os.LookupEnv("DB_NAME_SUFFIX")
	if !ok {
		suffix = defaultNameSuffix
	}
	return os.Getenv("DB_NAME_PREFIX") + service + suffix
}

// BuildURL assembles a postgres:// URL, escaping the credentials and the
// database name so passwords with reserved characters stay valid.
func BuildURL(host, port, user, password, dbName string, params url.Values) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + dbName,
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	return u.String()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
