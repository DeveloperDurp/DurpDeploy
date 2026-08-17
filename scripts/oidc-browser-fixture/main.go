//go:build oidctest

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type commandConfig struct {
	readyFile string
	selfTest  bool
	outage    bool
}

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := runCommand(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCommand(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("oidc-browser-fixture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	readyFile := flags.String("ready-file", "", "path for readiness JSON")
	selfTest := flags.Bool(
		"self-test",
		false,
		"verify the local TLS OIDC flow and exit",
	)
	outage := flags.Bool(
		"outage",
		false,
		"stop only the fake IdP after the harness becomes ready",
	)
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	config := commandConfig{
		readyFile: *readyFile,
		selfTest:  *selfTest,
		outage:    *outage,
	}
	if config.selfTest {
		return runSelfTest(ctx)
	}
	if config.readyFile == "" {
		return errors.New("--ready-file is required unless --self-test is set")
	}
	return runHarness(ctx, config)
}
