//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// Resolve the endpoint through the same SDK as the test fixtures, including
// Testcontainers properties and rootless socket discovery.
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	api, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve test container provider:", err)
		os.Exit(1)
	}
	host := api.DaemonHost()
	if err := api.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close test container provider:", err)
		os.Exit(1)
	}
	if _, err := fmt.Println(host); err != nil {
		fmt.Fprintln(os.Stderr, "write test container provider:", err)
		os.Exit(1)
	}
}
