// Package oratest provides isolated Oracle schemas for tests. It is used
// when OBS_TEST_ORACLE_ADMIN_URL is set (scripts/dev-oracle.sh prints it);
// otherwise tests skip, or fail when OBS_TEST_REQUIRE_ORACLE=1.
package oratest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/store/oracle"
)

// adminURL is an Oracle account allowed to create and drop users, for
// example oracle://system:<password>@127.0.0.1:15210/FREEPDB1 against the
// Oracle Database Free container started by scripts/dev-oracle.sh.
func adminURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("OBS_TEST_ORACLE_ADMIN_URL")
	if u == "" {
		if os.Getenv("OBS_TEST_REQUIRE_ORACLE") == "1" {
			t.Fatal("OBS_TEST_REQUIRE_ORACLE=1 but OBS_TEST_ORACLE_ADMIN_URL is not set")
		}
		t.Skip("set OBS_TEST_ORACLE_ADMIN_URL to run the Oracle store contract (scripts/dev-oracle.sh)")
	}
	return u
}

// NewSchema creates a throwaway Oracle user (= schema), unmigrated.
func NewSchema(t testing.TB) *oracle.Store {
	t.Helper()
	ctx := context.Background()
	admin, err := oracle.Open(ctx, adminURL(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	user := "OBS_T_" + strings.ToUpper(hex.EncodeToString(b))
	pw := "Pw" + hex.EncodeToString(b) + "x9"
	for _, stmt := range []string{
		`CREATE USER ` + user + ` IDENTIFIED BY "` + pw + `" QUOTA UNLIMITED ON USERS`,
		`GRANT CREATE SESSION, CREATE TABLE, CREATE SEQUENCE TO ` + user,
	} {
		if _, err := admin.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("creating test user: %v", err)
		}
	}
	u, _ := url.Parse(adminURL(t))
	u.User = url.UserPassword(user, pw)
	st, err := oracle.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = admin.DB().ExecContext(c, `DROP USER `+user+` CASCADE`)
		admin.Close()
	})
	return st
}

// New returns a migrated store in a throwaway schema.
func New(t testing.TB) *oracle.Store {
	t.Helper()
	st := NewSchema(t)
	if _, err := st.MigrateUp(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}
