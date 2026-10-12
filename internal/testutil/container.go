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
func CleanupContainer(t testing.TB, container testcontainers.Container) {
	t.Helper()
	if container == nil {
		return
	}
	value := reflect.ValueOf(container)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := container.Terminate(ctx); err != nil &&
			!errdefs.IsNotFound(err) {
			t.Errorf("remove test container: %v", err)
		}
	})
}
