package repository

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestTemplateLibraryLanguageRedirects(t *testing.T) {
	for _, language := range []struct{ suffix, interpreter string }{
		{suffix: "powershell", interpreter: "pwsh"},
		{suffix: "python", interpreter: "python3"},
	} {
		t.Run(language.interpreter, func(t *testing.T) {
			if _, err := exec.LookPath(language.interpreter); err != nil {
				t.Skip(
					"host interpreter unavailable; container E2E runs this interpreter",
				)
			}
			checkLibraryRedirects(t, language.suffix, language.interpreter)
		})
	}
}

func checkLibraryRedirects(t *testing.T, suffix, interpreter string) {
	t.Helper()
	var leaked atomic.Bool
	receiver := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				leaked.Store(true)
			}
			_, _ = w.Write([]byte("cross-origin bytes"))
		}),
	)
	defer receiver.Close()
	source := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer fixture-secret" {
				w.WriteHeader(401)
				return
			}
			switch r.URL.Path {
			case "/cross":
				http.Redirect(w, r, receiver.URL, http.StatusFound)
			case "/loop":
				http.Redirect(w, r, "/loop", http.StatusFound)
			case "/plaintext":
				http.Redirect(w, r, "http://example.test/", http.StatusFound)
			case "/truncated":
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("short"))
			default:
				w.WriteHeader(http.StatusNotModified)
			}
		}),
	)
	defer source.Close()
	root := t.TempDir()
	certificate := filepath.Join(root, "ca.pem")
	writeLibraryFixture(t, certificate, string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: source.Certificate().Raw,
	})), 0600)
	entry, err := GetLibraryTemplate("artifact-download-" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"DURPDEPLOY_STAGE_DIR=" + root,
		"ARTIFACT_TOKEN=fixture-secret",
		"SSL_CERT_FILE=" + certificate,
	}
	runLibraryInterpreter(t, interpreter, entry.Snapshot.Steps[0].ScriptBody,
		append(env, "ARTIFACT_URL="+source.URL+"/cross"), true)
	if leaked.Load() {
		t.Fatal("redirect leaked bearer token to a different origin")
	}
	for _, path := range []string{"/loop", "/plaintext", "/truncated", "/304"} {
		runLibraryInterpreter(
			t,
			interpreter,
			entry.Snapshot.Steps[0].ScriptBody,
			append(env, "ARTIFACT_URL="+source.URL+path),
			false,
		)
		data, err := os.ReadFile(filepath.Join(root, "artifact"))
		if err != nil || string(data) != "cross-origin bytes" {
			t.Fatal("failed download changed artifact", path, err)
		}
	}
}
