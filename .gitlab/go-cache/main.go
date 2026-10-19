// This build-only probe warms locked dependencies, without running their code.
package main

import (
	_ "net/http/httptest"
	_ "testing"

	_ "github.com/a-h/templ"
	_ "github.com/coreos/go-oidc/v3/oidc"
	_ "github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/pressly/goose/v3"
	_ "github.com/testcontainers/testcontainers-go"
	_ "modernc.org/sqlite"
)

func main() {}
