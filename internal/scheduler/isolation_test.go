package scheduler_test

import (
	"os"
	"testing"

	"durpdeploy/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
