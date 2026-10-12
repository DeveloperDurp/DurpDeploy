package main

import (
	"reflect"
	"testing"
)

func TestServerStateKeyRotation(t *testing.T) {
	for _, store := range serverStateStores {
		t.Run(store.name, func(t *testing.T) {
			f := newServerStateKeyFixture(t)
			value, err := f.rotation.oldBox.Encrypt("server-state-secret")
			if err != nil {
				t.Fatal(err)
			}
			store.seed(t, f, value)
			before := rotationMetadata(t, f.repo.DB, store.metadata)
			if err := f.rotate(t, false); err != nil {
				t.Fatal(err)
			}
			ciphertext := store.ciphertext(t, f)
			plain, err := f.rotation.newBox.Decrypt(ciphertext)
			if err != nil || plain != "server-state-secret" {
				t.Fatal("new key cannot recover state", err)
			}
			if _, err := f.rotation.oldBox.Decrypt(ciphertext); err == nil {
				t.Fatal("old key still decrypts state")
			}
			if after := rotationMetadata(
				t,
				f.repo.DB,
				store.metadata,
			); !reflect.DeepEqual(
				before,
				after,
			) {
				t.Fatal("rotation changed metadata")
			}
		})
	}
}

func TestServerStateRotationRejectsPlaintextAndRollsBack(t *testing.T) {
	for _, store := range serverStateStores {
		for _, plaintext := range []bool{false, true} {
			t.Run(
				store.name+map[bool]string{false: "/corrupt", true: "/plaintext"}[plaintext],
				func(t *testing.T) {
					f := newServerStateKeyFixture(t)
					earlier := seedVariableRotationSnapshot(
						t,
						f.repo,
						f.rotation.oldBox,
					)
					value := "corrupt-ciphertext"
					if plaintext {
						var err error
						value, err = f.rotation.oldBox.Encrypt("encrypted-only")
						if err != nil {
							t.Fatal(err)
						}
						// Keep earlier encrypted-only stores empty so the selected store rejects.
						if _, err := f.repo.DB.ExecContext(
							t.Context(),
							"DELETE FROM deployment_variable_snapshots",
						); err != nil {
							t.Fatal(err)
						}
					}
					store.seed(t, f, value)
					if err := f.rotate(t, plaintext); err == nil {
						t.Fatal("unsafe rotation succeeded")
					}
					if got := store.ciphertext(t, f); got != value {
						t.Fatal("failed rotation changed state")
					}
					if !plaintext {
						after, err := f.repo.Queries.GetDeploymentVariableSnapshot(
							t.Context(),
							earlier.DeploymentID,
						)
						if err != nil || after != earlier {
							t.Fatal(
								"failed rotation changed earlier snapshot",
								err,
							)
						}
					}
				},
			)
		}
	}
}
