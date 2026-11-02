package handler

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func TestLintBearerWarningParityAndBounds(t *testing.T) {
	scripts := []string{
		"", "#!/bin/sh\nif then", "echo $value", "#!anything\n$one $two",
		"#!/bin/sh\n# $comment\necho \"$quoted\" \\$escaped ${braced}",
		"\ufeff#!/bin/sh\r\n$first\r$second\n$third", "#!\r\n$x\n",
		"#!\n$" + strings.Repeat("x", MaxBearerLintScriptBytes-5),
		strings.Repeat("\n", MaxBearerLintScriptBytes),
	}
	random := rand.New(rand.NewPCG(177, 58))
	for range 100 {
		var script strings.Builder
		for range random.IntN(150) {
			fmt.Fprintf(
				&script,
				"%s$var_%d%s",
				[]string{"#", "\"", "\\", "\n"}[random.IntN(4)],
				random.IntN(
					100,
				),
				[]string{" ", "\n", "\r", "\""}[random.IntN(4)],
			)
		}
		scripts = append(scripts, script.String())
	}
	for i, script := range scripts {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			assertBearerWarningParity(t, script)
		})
	}
	for _, prefix := range []string{"#!\n", ""} {
		for _, count := range []int{99, 100, 101} {
			script := prefix + strings.Repeat("$secret_identifier ", count)
			got, truncated, err := LintBearerWarnings(t.Context(), script)
			findings := count
			if prefix == "" {
				findings++
			}
			if err != nil || len(got) != min(findings, 100) ||
				truncated != (findings > 100) {
				t.Fatalf(
					"cap %q/%d: %d %v %v",
					prefix,
					count,
					len(got),
					truncated,
					err,
				)
			}
		}
	}
}

func assertBearerWarningParity(t *testing.T, script string) {
	t.Helper()
	want := lintScript(script)
	got, truncated, err := LintBearerWarnings(t.Context(), script)
	if err != nil || got == nil || len(got) != min(len(want), 100) ||
		truncated != (len(want) > 100) {
		t.Fatalf("result length=%d truncated=%v error=%v",
			len(got), truncated, err)
	}
	for n, diagnostic := range got {
		if diagnostic.Line != want[n].Line || diagnostic.Code != want[n].Code ||
			diagnostic.Level != want[n].Level || len(diagnostic.Message) > 128 ||
			strings.Contains(diagnostic.Message, "var_") {
			t.Fatalf("warning %d differs: %+v", n, diagnostic)
		}
	}
}

func TestLintBearerRejectsInvalidInputAndStoppedWork(t *testing.T) {
	for _, script := range []string{strings.Repeat("x", 65_537), "\xff", "a\x00b"} {
		got, truncated, err := LintBearerWarnings(t.Context(), script)
		if !errors.Is(err, ErrBearerLintInput) || got != nil || truncated {
			t.Fatalf("invalid input produced %v %v %v", got, truncated, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deadline, stop := context.WithDeadline(
		t.Context(),
		time.Now().Add(-time.Second),
	)
	defer stop()
	for _, ctx := range []context.Context{ctx, deadline} {
		got, truncated, err := LintBearerWarnings(ctx, "#!\n$secret")
		if !errors.Is(err, ctx.Err()) || got != nil || truncated {
			t.Fatalf("stopped work produced %v %v %v", got, truncated, err)
		}
	}
}

func BenchmarkLintBearerMaximumInput(b *testing.B) {
	for name, script := range map[string]string{
		"identifier": "#!\n$" + strings.Repeat("x", 65_532),
		"lines":      strings.Repeat("\n", 65_536),
		"warnings":   "#!\n" + strings.Repeat("$x ", 21_844),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(script)))
			for b.Loop() {
				if _, _, err := LintBearerWarnings(b.Context(), script); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
