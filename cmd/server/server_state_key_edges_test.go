package main

import (
	"database/sql"
	"encoding/base64"
	"io"
	"os"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestServerStateRotationPreservesEmptyAndNull(t *testing.T) {
	for _, store := range serverStateStores {
		for _, empty := range []sql.NullString{{}, {String: "", Valid: true}} {
			if !empty.Valid &&
				(store.name == "totp" || store.enrollment || store.name == "project-template") {
				continue
			}
			t.Run(
				store.name+map[bool]string{false: "/null", true: "/empty"}[empty.Valid],
				func(t *testing.T) { checkEmptyServerStateRotation(t, store, empty) },
			)
		}
	}
}

func checkEmptyServerStateRotation(
	t *testing.T,
	store serverStateStore,
	empty sql.NullString,
) {
	t.Helper()
	f := newServerStateKeyFixture(t)
	if store.enrollment {
		store.seed(t, f, "")
	} else if _, err := f.repo.DB.ExecContext(t.Context(), store.insert, empty); err != nil {
		t.Fatal(err)
	}
	var before sql.NullString
	if err := f.repo.DB.QueryRowContext(t.Context(), store.read).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.rotate(t, false); err != nil {
		t.Fatal(err)
	}
	var after sql.NullString
	if err := f.repo.DB.QueryRowContext(t.Context(), store.read).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("rotation changed empty representation")
	}
}

func TestTOTPEnrollmentRotationRejectsMalformedCeremony(t *testing.T) {
	for _, ceremony := range []string{"{", "null", "{}", `{"encrypted_seed":42}`, `{"encrypted_seed":"invalid"}`} {
		t.Run(ceremony, func(t *testing.T) {
			f := newServerStateKeyFixture(t)
			earlier := seedVariableRotationSnapshot(
				t,
				f.repo,
				f.rotation.oldBox,
			)
			store := serverStateStores[1]
			if _, err := f.repo.DB.ExecContext(
				t.Context(),
				store.insert,
				ceremony,
			); err != nil {
				t.Fatal(err)
			}
			if err := f.rotate(t, false); err == nil {
				t.Fatal("malformed ceremony rotation succeeded")
			}
			after, err := f.repo.Queries.GetDeploymentVariableSnapshot(
				t.Context(),
				earlier.DeploymentID,
			)
			if err != nil || after != earlier {
				t.Fatal("failed rotation changed earlier snapshot", err)
			}
		})
	}
}

func TestMFAKeyRotationPreservesOtherCeremonies(t *testing.T) {
	f := newServerStateKeyFixture(t)
	const ceremony = `{"encrypted_seed":null,"other":[1,2]}`
	store := serverStateStores[1]
	if _, err := f.repo.DB.ExecContext(
		t.Context(),
		store.insert,
		ceremony,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(
		t.Context(),
		"UPDATE mfa_challenges SET purpose = 'webauthn_register'",
	); err != nil {
		t.Fatal(err)
	}
	if err := f.rotate(t, false); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := f.repo.DB.QueryRowContext(t.Context(), store.read).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != ceremony {
		t.Fatal("rotation changed unrelated ceremony")
	}
	if rotated, err := rotateTOTPEnrollment(
		ceremony,
		f.rotation,
	); err != nil ||
		rotated != ceremony {
		t.Fatal("rotation changed null seed", err)
	}
}

func TestRunSecretKeyRotateIncludesServerState(t *testing.T) {
	f := newServerStateKeyFixture(t)
	for _, store := range serverStateStores {
		value, err := f.rotation.oldBox.Encrypt(store.name)
		if err != nil {
			t.Fatal(err)
		}
		store.seed(t, f, value)
	}
	box := runServerStateRotationCLI(t, f, false)
	for _, store := range serverStateStores {
		plain, err := box.Decrypt(store.ciphertext(t, f))
		if err != nil || plain != store.name {
			t.Fatalf("%s state lost: %v", store.name, err)
		}
	}
}

func runServerStateRotationCLI(
	t *testing.T,
	f serverStateKeyFixture,
	plaintext bool,
) *secret.Box {
	t.Helper()
	var seq int
	var database, dsn string
	if err := f.repo.DB.QueryRowContext(t.Context(), "PRAGMA database_list").
		Scan(&seq, &database, &dsn); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_DB", dsn)
	t.Setenv(
		"DURPDEPLOY_SECRET_KEY",
		base64.StdEncoding.EncodeToString(make([]byte, 32)),
	)
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	args := []string{"rotate"}
	if plaintext {
		args = []string{"rotate", "--plaintext"}
	}
	code := runSecretKey(args)
	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("rotation CLI exited %d", code)
	}
	key, err := base64.StdEncoding.DecodeString(
		extractNewKey(t, string(output)),
	)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestServerStateRotationAllowsLegacyPlaintextVariables(t *testing.T) {
	f := newServerStateKeyFixture(t)
	variable, err := f.repo.Queries.CreateVariable(
		t.Context(),
		db.CreateVariableParams{
			ProjectID: 1,
			Name:      "legacy",
			Value:     sql.NullString{String: "legacy-secret", Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	box := runServerStateRotationCLI(t, f, true)
	var ciphertext string
	if err := f.repo.DB.QueryRowContext(t.Context(), "SELECT value FROM variables WHERE id = ?", variable.ID).
		Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := box.Decrypt(ciphertext)
	if err != nil || plain != "legacy-secret" {
		t.Fatal("legacy plaintext migration lost value", err)
	}
}
