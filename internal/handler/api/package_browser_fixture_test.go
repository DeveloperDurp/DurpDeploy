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

func packageBrowserEndpoint(t *testing.T) string {
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
		"harbor.durp.info/microsoft/playwright:v1.61.1-noble",
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
	startupError := make(chan error, 1)
	startupLog := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		var diagnostics string
		started := false
		for scanner.Scan() {
			diagnostics += scanner.Text() + "\n"
			if len(diagnostics) > 8192 {
				diagnostics = diagnostics[len(diagnostics)-8192:]
			}
			select {
			case <-startupLog:
			default:
			}
			startupLog <- diagnostics
			if endpoint, ok := strings.CutPrefix(
				scanner.Text(),
				"DevTools listening on ",
			); ok && !started {
				ready <- endpoint
				started = true
			}
		}
		if !started {
			startupError <- fmt.Errorf(
				"Chromium exited before readiness: %v\n%s",
				scanner.Err(), diagnostics,
			)
		}
	}()
	var endpoint string
	select {
	case endpoint = <-ready:
	case err := <-startupError:
		t.Fatal(err)
	case <-time.After(time.Minute):
		select {
		case diagnostics := <-startupLog:
			t.Log(diagnostics)
		default:
		}
		t.Fatal("Chromium did not expose its test endpoint")
	}
	return endpoint
}

// Tests that change browser-wide settings request their own process.
// HTTPS certificate overrides affect the process, so keep those browsers private.
func startLivePackageBrowser(t *testing.T, base string) *packageBrowser {
	t.Helper()
	private := strings.HasPrefix(strings.ToLower(base), "https://")
	b := startPackageBrowser(t, private)
	if private {
		b.call(t, "Security.setIgnoreCertificateErrors",
			map[string]bool{"ignore": true}, &struct{}{})
	}
	return b
}

func startPackageBrowser(t *testing.T, privateProcess ...bool) *packageBrowser {
	t.Helper()
	endpoint := os.Getenv("DURPDEPLOY_TEST_BROWSER_ENDPOINT")
	if len(privateProcess) != 0 && privateProcess[0] {
		endpoint = ""
	}
	if endpoint == "" {
		endpoint = packageBrowserEndpoint(t)
	}
	wire, err := connectPackageBrowser(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wire.connection.Close() })
	var isolated struct {
		ID string `json:"browserContextId"`
	}
	if err := wire.call("Target.createBrowserContext", "",
		map[string]bool{"disposeOnDetach": true}, &isolated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := wire.call("Target.disposeBrowserContext", "",
			map[string]string{"browserContextId": isolated.ID},
			&struct{}{}); err != nil {
			t.Errorf("dispose test browser context: %v", err)
		}
	})
	var created struct {
		Target string `json:"targetId"`
	}
	if err := wire.call(
		"Target.createTarget",
		"",
		map[string]string{
			"url":              "about:blank",
			"browserContextId": isolated.ID,
		},
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
		t.Fatalf("browser evaluation failed: %s", result.Exception)
	}
	return result.Result.Value
}

func (b *packageBrowser) wait(t *testing.T, predicate string) {
	t.Helper()
	b.waitFor(t, predicate, 15*time.Second)
}

func (b *packageBrowser) waitFor(
	t *testing.T,
	predicate string,
	timeout time.Duration,
) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		var result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			Exception json.RawMessage `json:"exceptionDetails"`
		}
		err := b.wire.call("Runtime.evaluate", b.session, map[string]any{
			// A new document can expose its URL before its DOM is parsed.
			"expression": fmt.Sprintf(
				"document.readyState !== 'loading' && Boolean(%s)", predicate,
			),
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
	b.evaluate(t, `(async () => {
		const frame = () => new Promise(requestAnimationFrame);
		const settle = async () => { await frame(); await frame(); };
		let timeout;
		try {
			return await Promise.race([
				(async () => {
					await document.fonts.ready;
					for (let y = 0; y < document.documentElement.scrollHeight; y += innerHeight) {
						window.scrollTo({top: y, left: 0, behavior: 'instant'});
						await settle();
					}
					window.scrollTo({top: 0, left: 0, behavior: 'instant'});
					let previous = '';
					for (;;) {
						await settle();
						const size = JSON.stringify([
							document.documentElement.scrollWidth,
							document.documentElement.scrollHeight,
							innerWidth, innerHeight, scrollX, scrollY
						]);
						const images = [...document.images].every(image =>
							!image.getClientRects().length || image.complete);
						const animations = document.getAnimations().every(animation =>
							animation.effect?.getTiming().iterations === Infinity ||
							(!animation.pending && animation.playState !== 'running'));
						if (images && animations && document.fonts.status === 'loaded' &&
							!document.querySelector('.htmx-settling') &&
							size === previous && scrollY === 0) return true;
						previous = size;
					}
				})(),
				new Promise((_, reject) => {
					timeout = setTimeout(() => reject(new Error(
						'Screenshot rendering did not settle')), 15000);
				})
			]);
		} finally {
			clearTimeout(timeout);
		}
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
