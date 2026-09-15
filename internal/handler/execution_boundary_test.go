package handler_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Setenv(
		"DURPDEPLOY_EXECUTION_BOUNDARY",
		"development",
	); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
