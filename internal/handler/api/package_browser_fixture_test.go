//go:build e2e && (packagebrowser || agentbrowser)

package api_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type packageBrowser struct {
	wire    *packageBrowserWire
	session string
}

func startPackageBrowser(t *testing.T) *packageBrowser {
	t.Helper()
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if kind == "" {
		kind = "docker"
		if _, err := exec.LookPath("podman"); err == nil {
			kind = "podman"
		}
	}
	name := "package-browser-" + uuid.NewString()
	command := exec.CommandContext(
		t.Context(),
		kind,
		"run",
		"--rm",
		"--pull=missing",
		"--name="+name,
		"--network=host",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--shm-size=256m",
		"--entrypoint=/bin/bash",
		"mcr.microsoft.com/playwright:v1.61.1-noble",
		"-lc",
		"exec /ms-playwright/chromium-*/chrome-linux*/chrome --headless --no-sandbox --disable-dev-shm-usage --remote-debugging-address=127.0.0.1 --remote-debugging-port=0 --user-data-dir=/tmp/package-browser about:blank",
	)
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, kind, "rm", "--force", name).
			CombinedOutput(); err != nil {
			t.Errorf("remove test browser: %v: %s", err, output)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("test browser client did not exit")
		}
	})
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if endpoint, ok := strings.CutPrefix(
				scanner.Text(),
				"DevTools listening on ",
			); ok {
				ready <- endpoint
			}
		}
	}()
	var endpoint string
	select {
	case endpoint = <-ready:
	case <-time.After(time.Minute):
		t.Fatal("Chromium did not expose its test endpoint")
	}
	wire, err := connectPackageBrowser(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wire.connection.Close() })
	var created struct {
		Target string `json:"targetId"`
	}
	if err := wire.call(
		"Target.createTarget",
		"",
		map[string]string{"url": "about:blank"},
		&created,
	); err != nil {
		t.Fatal(err)
	}
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := wire.call(
		"Target.attachToTarget",
		"",
		map[string]any{"targetId": created.Target, "flatten": true},
		&attached,
	); err != nil {
		t.Fatal(err)
	}
	browser := &packageBrowser{wire, attached.Session}
	browser.call(t, "Page.enable", struct{}{}, &struct{}{})
	browser.call(t, "Network.enable", struct{}{}, &struct{}{})
	return browser
}

func (b *packageBrowser) call(
	t *testing.T,
	method string,
	parameters any,
	result any,
) {
	t.Helper()
	if err := b.wire.call(method, b.session, parameters, result); err != nil {
		t.Fatalf("browser %s: %v", method, err)
	}
}

func (b *packageBrowser) evaluate(
	t *testing.T,
	expression string,
) json.RawMessage {
	t.Helper()
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	b.call(
		t,
		"Runtime.evaluate",
		map[string]any{
			"expression":    expression,
			"awaitPromise":  true,
			"returnByValue": true,
		},
		&result,
	)
	if len(result.Exception) != 0 {
		t.Fatal("browser evaluation failed")
	}
	return result.Result.Value
}

func (b *packageBrowser) wait(t *testing.T, predicate string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		var result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			Exception json.RawMessage `json:"exceptionDetails"`
		}
		err := b.wire.call("Runtime.evaluate", b.session, map[string]any{
			"expression":    fmt.Sprintf("Boolean(%s)", predicate),
			"returnByValue": true,
		}, &result)
		// A navigation destroys promises tied to the old document. Poll from
		// the driver instead, allowing only that transient context failure.
		if err != nil &&
			!strings.Contains(
				err.Error(),
				"Inspected target navigated or closed",
			) {
			t.Fatal(err)
		}
		if err == nil {
			if len(result.Exception) != 0 {
				t.Fatalf(
					"browser predicate evaluation failed: %s",
					result.Exception,
				)
			}
			if string(result.Result.Value) == "true" {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("browser condition timed out: %s", predicate)
}

func (b *packageBrowser) screenshot(t *testing.T, name string) {
	t.Helper()
	b.evaluate(
		t,
		`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))))`,
	)
	b.evaluate(t, `(async () => {
		await document.fonts.ready;
		for (let y = 0; y < document.documentElement.scrollHeight; y += innerHeight) {
			window.scrollTo(0, y); await new Promise(resolve => setTimeout(resolve, 50));
		}
		window.scrollTo(0, 0); await new Promise(resolve => setTimeout(resolve, 300));
		return true;
	})()`)
	directory := os.Getenv("DURPDEPLOY_PACKAGE_UI_EVIDENCE")
	if directory == "" {
		directory = t.TempDir()
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data string `json:"data"`
	}
	var clip struct {
		X, Y, Width, Height, Scale float64
	}
	if err := json.Unmarshal(
		b.evaluate(
			t,
			`({x: 0, y: 0, width: innerWidth, height: document.querySelector('dialog[open]') ? innerHeight : Math.max(innerHeight, document.documentElement.scrollHeight), scale: 1})`,
		),
		&clip,
	); err != nil {
		t.Fatal(err)
	}
	b.call(
		t,
		"Page.captureScreenshot",
		map[string]any{
			"format":                "png",
			"captureBeyondViewport": true,
			"clip": map[string]float64{
				"x":      clip.X,
				"y":      clip.Y,
				"width":  clip.Width,
				"height": clip.Height,
				"scale":  clip.Scale,
			},
		},
		&result,
	)
	image, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, name+".png"),
		image,
		0600,
	); err != nil {
		t.Fatal(err)
	}
}
