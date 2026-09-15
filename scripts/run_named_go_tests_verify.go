//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type testEvent struct {
	Action string
	Test   string
}

type eventCounts struct {
	terminal int
	pass     int
	skip     int
	fail     int
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "no required test events")
		os.Exit(2)
	}
	required := make(map[string]eventCounts, len(os.Args)-1)
	for _, name := range os.Args[1:] {
		if name == "" {
			fmt.Fprintln(
				os.Stderr,
				"required test event names must not be empty",
			)
			os.Exit(2)
		}
		if _, exists := required[name]; exists {
			fmt.Fprintf(os.Stderr, "duplicate required test event: %s\n", name)
			os.Exit(2)
		}
		required[name] = eventCounts{}
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"invalid go test JSON event at line %d: %v\n",
				line,
				err,
			)
			os.Exit(1)
		}
		counts, wanted := required[event.Test]
		if !wanted {
			continue
		}
		switch event.Action {
		case "pass":
			counts.terminal++
			counts.pass++
		case "skip":
			counts.terminal++
			counts.skip++
		case "fail":
			counts.terminal++
			counts.fail++
		}
		required[event.Test] = counts
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read go test JSON events: %v\n", err)
		os.Exit(1)
	}

	valid := true
	for _, name := range os.Args[1:] {
		counts := required[name]
		if counts.terminal != 1 || counts.pass != 1 ||
			counts.skip != 0 || counts.fail != 0 {
			fmt.Fprintf(
				os.Stderr,
				"required test event %s: terminal=%d pass=%d skip=%d fail=%d\n",
				name,
				counts.terminal,
				counts.pass,
				counts.skip,
				counts.fail,
			)
			valid = false
		}
	}
	if !valid {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "named-go-tests: PASS required=%d\n", len(required))
}
