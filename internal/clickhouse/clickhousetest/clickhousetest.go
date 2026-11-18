// Package clickhousetest gives integration tests a throwaway ClickHouse database.
package clickhousetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// URLEnv names the admin URL of the ClickHouse server integration tests use.
const URLEnv = "LOCO_TEST_CLICKHOUSE_URL"

const nameBytes = 4

// Database is a uniquely named database on the test server and an admin connection to it.
type Database struct {
	DSN   string
	Name  string
	Admin driver.Conn
}

// New skips the test without URLEnv. Otherwise it names a database for the test to create, and
// drops it and closes the admin connection when the test ends.
func New(tb testing.TB, prefix string) *Database {
	tb.Helper()
	dsn := os.Getenv(URLEnv)
	if dsn == "" {
		tb.Skip(URLEnv + " not set")
	}
	options, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		tb.Fatalf("parse %s: %v", URLEnv, err)
	}
	admin, err := clickhouse.Open(options)
	if err != nil {
		tb.Fatalf("open clickhouse: %v", err)
	}
	suffix := make([]byte, nameBytes)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatalf("random database name: %v", err)
	}
	database := &Database{DSN: dsn, Name: prefix + hex.EncodeToString(suffix), Admin: admin}
	tb.Cleanup(func() {
		if err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+database.Name+" SYNC"); err != nil {
			tb.Errorf("drop %s: %v", database.Name, err)
		}
		if err := admin.Close(); err != nil {
			tb.Errorf("close clickhouse: %v", err)
		}
	})
	return database
}

// User creates a user with grants on the database, dropped when the test ends, and returns its URL.
func (d *Database) User(tb testing.TB, grants string) string {
	tb.Helper()
	user := d.Name + "_user"
	password := hex.EncodeToString([]byte(user))
	for _, statement := range []string{
		"CREATE USER " + user + " IDENTIFIED WITH plaintext_password BY '" + password + "'",
		"GRANT " + grants + " ON " + d.Name + ".* TO " + user,
	} {
		if err := d.Admin.Exec(tb.Context(), statement); err != nil {
			tb.Fatalf("%s: %v", statement, err)
		}
	}
	tb.Cleanup(func() {
		if err := d.Admin.Exec(context.Background(), "DROP USER IF EXISTS "+user); err != nil {
			tb.Errorf("drop %s: %v", user, err)
		}
	})
	parsed, err := url.Parse(d.DSN)
	if err != nil {
		tb.Fatalf("parse %s: %v", URLEnv, err)
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String()
}

// Rejecting serves ClickHouse's HTTP interface, failing every request with the named exception,
// and counts the requests it receives.
func Rejecting(tb testing.TB, code int, name string) (string, *atomic.Int32) {
	tb.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Clickhouse-Exception-Code", strconv.Itoa(code))
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Code: %d. DB::Exception: rejected. (%s)\n", code, name)
	}))
	tb.Cleanup(server.Close)
	return server.URL, &requests
}
