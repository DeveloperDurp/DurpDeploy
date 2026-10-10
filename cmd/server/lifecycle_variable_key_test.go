package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestLifecycleVariablesRotateAtomically(t *testing.T) {
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "rotation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	oldBox, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(oldBox)
	key := make([]byte, 32)
	key[0] = 1
	newBox, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "rotation"},
	)
	if err != nil {
		t.Fatal(err)
	}
	v, err := repo.SaveLifecycleVariable(
		t.Context(),
		repository.LifecycleVariableInput{
			CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: lc.ID,
				Name:        "TOKEN",
				Value: sql.NullString{
					String: "rotate-secret",
					Valid:  true,
				},
				Secret: 1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ids := db.GetLifecycleVariableParams{ID: v.ID, LifecycleID: lc.ID}
	before, err := repo.Queries.GetLifecycleVariable(t.Context(), ids)
	if err != nil {
		t.Fatal(err)
	}
	corrupt, err := repo.Queries.CreateLifecycleVariable(
		t.Context(),
		db.CreateLifecycleVariableParams{
			LifecycleID: lc.ID,
			Name:        "CORRUPT",
			Value:       sql.NullString{String: "not-ciphertext", Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rotate := func(plaintext bool) error {
		return repo.WithTx(t.Context(), func(q *db.Queries) error {
			return rotateStoredCredentials(
				t.Context(),
				q,
				secretKeyRotation{
					oldBox:    oldBox,
					newBox:    newBox,
					plaintext: plaintext,
				},
			)
		})
	}
	if err := rotate(false); err == nil {
		t.Fatal("corrupt ciphertext rotation succeeded")
	}
	after, err := repo.Queries.GetLifecycleVariable(t.Context(), ids)
	if err != nil || before.Value != after.Value {
		t.Fatal("failed rotation changed earlier row", err)
	}
	if _, err := repo.Queries.DeleteLifecycleVariable(t.Context(), db.DeleteLifecycleVariableParams{ID: corrupt.ID, LifecycleID: lc.ID}); err != nil {
		t.Fatal(err)
	}
	if err := rotate(true); err == nil {
		t.Fatal("plaintext rotation accepted encrypted variables")
	}
	if err := rotate(false); err != nil {
		t.Fatal(err)
	}
	after, err = repo.Queries.GetLifecycleVariable(t.Context(), ids)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := newBox.Decrypt(after.Value.String)
	if err != nil || plain != "rotate-secret" {
		t.Fatal("new key cannot recover value", err)
	}
	if _, err := oldBox.Decrypt(after.Value.String); err == nil {
		t.Fatal("old key still decrypts value")
	}
}
