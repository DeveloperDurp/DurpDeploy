package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/testutil"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func serverStateParityDSN(t *testing.T, engine string) string {
	t.Helper()
	if engine == "mssql" {
		return testutil.SQLServerDSN(t)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	container, err := postgres.Run(
		ctx,
		"harbor.durp.info/dockerhub/library/postgres:16-alpine",
		postgres.WithDatabase("rotation"),
		postgres.WithUsername("rotation"),
		postgres.WithPassword("fixture-only"),
		postgres.BasicWaitStrategies(),
	)
	testutil.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func seedParityState(t *testing.T, f serverStateKeyFixture, engine string) {
	t.Helper()
	for _, store := range serverStateStores {
		value, err := f.rotation.oldBox.Encrypt(store.name)
		if err != nil {
			t.Fatal(err)
		}
		statement := store.insert
		zero := "0x" + strings.Repeat("00", 32)
		if engine == "postgres" {
			zero = "decode('" + strings.Repeat("00", 32) + "','hex')"
		}
		statement = strings.ReplaceAll(statement, "zeroblob(32)", zero)
		statement = strings.ReplaceAll(
			statement,
			"printf('%064d',1)",
			"'"+strings.Repeat("0", 63)+"1'",
		)
		if store.enrollment {
			encoded, err := json.Marshal(
				map[string]string{"encrypted_seed": value, "extra": "retained"},
			)
			if err != nil {
				t.Fatal(err)
			}
			value = string(encoded)
		}
		var errInsert error
		if store.name == "totp" {
			_, errInsert = f.repo.DB.ExecContext(
				t.Context(),
				statement,
				[]byte(value),
			)
		} else {
			_, errInsert = f.repo.DB.ExecContext(t.Context(), statement, value)
		}
		if errInsert != nil {
			t.Fatalf("seed %s: %v", store.name, errInsert)
		}
	}
}

func TestServerStateKeyRotationBackendParity(t *testing.T) {
	for _, engine := range []string{"postgres", "mssql"} {
		t.Run(engine, func(t *testing.T) {
			f := newServerStateKeyFixtureDSN(t, serverStateParityDSN(t, engine))
			seedParityState(t, f, engine)
			before := make([][]any, len(serverStateStores))
			ciphertext := make([]string, len(serverStateStores))
			for i, store := range serverStateStores {
				before[i] = rotationMetadata(t, f.repo.DB, store.metadata)
				ciphertext[i] = store.ciphertext(t, f)
			}
			// The last store must roll back all earlier ciphertext updates.
			if _, err := f.repo.DB.ExecContext(
				t.Context(),
				"UPDATE remote_step_runs SET log_buffer_ciphertext = ?",
				"invalid",
			); err != nil {
				t.Fatal(err)
			}
			if err := f.rotate(t, false); err == nil {
				t.Fatal("corrupt backend rotation succeeded")
			}
			for i, store := range serverStateStores[:len(serverStateStores)-1] {
				if store.ciphertext(t, f) != ciphertext[i] {
					t.Fatalf("%s changed after rollback", store.name)
				}
			}
			if _, err := f.repo.DB.ExecContext(
				t.Context(),
				"UPDATE remote_step_runs SET log_buffer_ciphertext = ?",
				ciphertext[len(ciphertext)-1],
			); err != nil {
				t.Fatal(err)
			}
			if err := f.rotate(t, true); err == nil {
				t.Fatal("backend plaintext rotation accepted encrypted state")
			}
			if err := f.rotate(t, false); err != nil {
				t.Fatal(err)
			}
			for i, store := range serverStateStores {
				value := store.ciphertext(t, f)
				plain, err := f.rotation.newBox.Decrypt(value)
				if err != nil || plain != store.name {
					t.Fatalf("%s new-key recovery failed: %v", store.name, err)
				}
				if _, err := f.rotation.oldBox.Decrypt(value); err == nil {
					t.Fatalf("%s old key still decrypts", store.name)
				}
				if after := rotationMetadata(
					t,
					f.repo.DB,
					store.metadata,
				); !reflect.DeepEqual(
					before[i],
					after,
				) {
					t.Fatalf("%s metadata changed", store.name)
				}
			}
		})
	}
}
