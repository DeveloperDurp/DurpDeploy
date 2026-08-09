//go:build mobilebrowser

package handler_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type mobileBrowserCleanup struct {
	ProfileRemoved         bool `json:"profileRemoved"`
	ProcessExited          bool `json:"processExited"`
	BrowserDisconnected    bool `json:"browserDisconnected"`
	ProfileProcessesExited bool `json:"profileProcessesExited"`
	ProcessGroupsExited    bool `json:"processGroupsExited"`
}

type mobileBrowserReport struct {
	Attempts     int                        `json:"attempts"`
	Cleanup      mobileBrowserCleanup       `json:"cleanup"`
	Measurements []mobileBrowserMeasurement `json:"measurements"`
}

type mobileBrowserMeasurement struct {
	Target             string   `json:"target"`
	InteractionFailure *string  `json:"interactionFailure"`
	Violations         []string `json:"violations"`
	SecretMask         *struct {
		HTMLAbsent  bool `json:"htmlAbsent"`
		TextAbsent  bool `json:"textAbsent"`
		MaskVisible bool `json:"maskVisible"`
	} `json:"secretMask"`
	ScreenshotSentinelAbsent bool `json:"screenshotSentinelAbsent"`
	Viewport                 struct {
		Name   string `json:"name"`
		Width  int    `json:"width"`
		Layout string `json:"layout"`
	} `json:"viewport"`
	Geometry struct {
		ClientWidth         int `json:"clientWidth"`
		DocumentWidth       int `json:"documentWidth"`
		BodyWidth           int `json:"bodyWidth"`
		OverflowingElements []struct {
			TagName   string `json:"tagName"`
			ClassName string `json:"className"`
		} `json:"overflowingElements"`
	} `json:"geometry"`
}

func TestMobileBrowserReadability(t *testing.T) {
	// Given
	root := repositoryRoot(t)
	requireMobileBrowserPrerequisites(t, root)
	fixtures := newMobileBrowserFixtures(t)
	reports := make(map[string]mobileBrowserReport, len(fixtures.sessions))

	// When
	for role, session := range fixtures.sessions {
		t.Run(role, func(t *testing.T) {
			profileReceipt := filepath.Join(
				fixtures.evidenceDir,
				role+".profile",
			)
			command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
			command.Dir = root
			command.Env = append(
				os.Environ(),
				mobileBrowserEnvironment(fixtures, role, session)...,
			)
			command.Env = append(
				command.Env,
				"NODE_PATH="+mobileBrowserNodeModules(root),
				"MOBILE_PROFILE_RECEIPT_FILE="+profileReceipt,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("mobile browser QA: %v\n%s", err, output)
			}
			assertMobileBrowserResourcesGone(
				t,
				readMobileBrowserProfile(t, profileReceipt),
			)
			reportPath := filepath.Join(fixtures.evidenceDir, role+".json")
			contents, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("read %s measurements: %v", role, err)
			}
			var report mobileBrowserReport
			if err := json.Unmarshal(contents, &report); err != nil {
				t.Fatalf("parse %s measurements: %v", role, err)
			}
			if !report.Cleanup.ProfileRemoved ||
				!report.Cleanup.ProcessExited ||
				!report.Cleanup.BrowserDisconnected ||
				!report.Cleanup.ProfileProcessesExited ||
				!report.Cleanup.ProcessGroupsExited {
				t.Fatalf(
					"incomplete %s cleanup receipt: %+v",
					role,
					report.Cleanup,
				)
			}
			if os.Getenv("MOBILE_FORCE_CONTEXT_CLOSE_ONCE") == "1" &&
				report.Attempts != 2 {
				t.Fatalf(
					"%s browser attempts = %d, want 2",
					role,
					report.Attempts,
				)
			}
			for _, measurement := range report.Measurements {
				if measurement.InteractionFailure != nil {
					t.Fatalf(
						"%s %s interaction failed: %s",
						role,
						measurement.Target,
						*measurement.InteractionFailure,
					)
				}
				if len(measurement.Violations) > 0 {
					t.Fatalf(
						"%s %s %dpx violations: %v",
						role,
						measurement.Target,
						measurement.Viewport.Width,
						measurement.Violations,
					)
				}
				if measurement.Viewport.Width == 375 &&
					measurement.Viewport.Layout != "mobile" {
					t.Fatalf(
						"%s %s 375px layout = %q, want mobile",
						role,
						measurement.Target,
						measurement.Viewport.Layout,
					)
				}
				if (measurement.Viewport.Width == 768 ||
					measurement.Viewport.Width == 1280) &&
					measurement.Viewport.Layout != "native" {
					t.Fatalf(
						"%s %s %dpx layout = %q, want native",
						role,
						measurement.Target,
						measurement.Viewport.Width,
						measurement.Viewport.Layout,
					)
				}
				if measurement.Target == "variables" &&
					(measurement.SecretMask == nil ||
						!measurement.SecretMask.HTMLAbsent ||
						!measurement.SecretMask.TextAbsent ||
						!measurement.SecretMask.MaskVisible ||
						!measurement.ScreenshotSentinelAbsent) {
					t.Fatalf("incomplete %s variable secret receipt", role)
				}
			}
			reports[role] = report
		})
	}
	receipt, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatalf("marshal browser cleanup receipts: %v", err)
	}
	evidenceFile := os.Getenv("MOBILE_EVIDENCE_FILE")
	if evidenceFile == "" {
		evidenceFile = "task-3-mobile-readability-receipt.json"
	}
	if err := writeMobileBrowserReceipt(
		root,
		evidenceFile,
		receipt,
	); err != nil {
		t.Fatalf("write durable browser cleanup receipts: %v", err)
	}

	// Then
	viewerWriteIsRejected(t, fixtures)
}

func requireMobileBrowserPrerequisites(t *testing.T, root string) {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("mobile browser prerequisite unavailable: node: %v", err)
	}
	nodeModules := mobileBrowserNodeModules(root)
	if _, err := os.Stat(
		filepath.Join(nodeModules, "playwright"),
	); err != nil {
		t.Fatalf("mobile browser prerequisite unavailable: Playwright: %v", err)
	}
	command := exec.Command(
		"node",
		"-e",
		"const { chromium } = require('playwright'); const fs = require('fs'); process.exit(fs.existsSync(chromium.executablePath()) ? 0 : 1)",
	)
	command.Dir = root
	command.Env = append(os.Environ(), "NODE_PATH="+nodeModules)
	if err := command.Run(); err != nil {
		t.Fatalf("Playwright Chromium prerequisite unavailable: %v", err)
	}
}

func mobileBrowserNodeModules(root string) string {
	if nodeModules := os.Getenv("MOBILE_BROWSER_NODE_MODULES"); nodeModules != "" {
		return nodeModules
	}
	return filepath.Join(root, "node_modules")
}

func TestMobileBrowserReadabilityRetriesClosedBrowser(t *testing.T) {
	// Given
	t.Setenv("MOBILE_TARGETS", "steps")
	t.Setenv("MOBILE_STRICT", "1")
	t.Setenv("MOBILE_FORCE_CONTEXT_CLOSE_ONCE", "1")

	// When / Then
	TestMobileBrowserReadability(t)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
