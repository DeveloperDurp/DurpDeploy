package main

import (
	"strings"
	"testing"
)

func TestLoadDSN_defaultsToImmediateSQLiteTransactions(t *testing.T) {
	// Given: no production database override.
	t.Setenv("DURPDEPLOY_DB", "")

	// When: the server loads its default SQLite DSN.
	dsn := loadDSN()

	// Then: write transactions acquire their lock before reading.
	if !strings.Contains(dsn, "_txlock=immediate") {
		t.Fatalf("loadDSN() = %q, want immediate SQLite transactions", dsn)
	}
}

func TestLoadDSN_addsSQLiteSafetyPragmasToPathOverride(t *testing.T) {
	// Given: a configured SQLite database path.
	t.Setenv("DURPDEPLOY_DB", "/var/lib/durpdeploy/durpdeploy.db")

	// When: the server loads the configured database.
	dsn := loadDSN()

	// Then: SQLite integrity and transaction settings remain enforced.
	if !strings.Contains(dsn, "_pragma=foreign_keys(1)") ||
		!strings.Contains(dsn, "_txlock=immediate") {
		t.Fatalf("loadDSN() = %q, want SQLite safety pragmas", dsn)
	}
}
