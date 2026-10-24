package api_test

import (
	"fmt"
	"os"
	"testing"

	"durpdeploy/internal/testenv"
)

var apiFixtureRoot string

func TestMain(m *testing.M) {
	var err error
	apiFixtureRoot, err = os.MkdirTemp(
		os.Getenv("DURPDEPLOY_GO_TEST_DIRECTORY"), "api-fixtures-",
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create API fixture root:", err)
		os.Exit(1)
	}
	status := testenv.Run(m)
	if err := os.RemoveAll(apiFixtureRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove API fixture root:", err)
		status = 1
	}
	os.Exit(status)
}
