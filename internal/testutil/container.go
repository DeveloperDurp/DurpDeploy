package testutil

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/testcontainers/testcontainers-go"
)

// CleanupContainer must be called before checking a container's startup error:
// Testcontainers can return both a container and an error. Nil handles are safe.
// Cleanup discards disposable fixtures without waiting for graceful shutdown.
func CleanupContainer(t testing.TB, container testcontainers.Container) {
	t.Helper()
	t.Cleanup(func() {
		if err := TerminateContainer(container); err != nil {
			t.Errorf("remove test container: %v", err)
		}
	})
}

// TerminateContainer removes an owned fixture with an independent deadline.
// Package fixtures call it from TestMain and fail the package on error.
func TerminateContainer(container testcontainers.Container) error {
	if container == nil {
		return nil
	}
	value := reflect.ValueOf(container)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := container.Terminate(ctx, testcontainers.StopTimeout(0))
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}
