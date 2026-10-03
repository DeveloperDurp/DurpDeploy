//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/testdns"
)

const verificationQueryCredential = "stored-query/token&proof value"
const verificationPathCredential = "stored-path/token&proof value"
const verificationHostCredential = "stored-host-credential"

func TestVerificationEncryptedTargetsE2E(t *testing.T) {
	for _, kind := range []string{"http", "bash"} {
		t.Run(kind, func(t *testing.T) {
			f := newVerificationE2E(t)
			target := encryptedVerificationTarget(t, kind)
			configureVerification(t, f, kind, target, 5)
			deployment := verificationDeploy(t, f,
				verificationRelease(t, f, "encrypted-"+kind))
			f.completion(t, deployment.ID, events.DeploymentSucceeded)
			assertEncryptedVerificationSnapshot(t, f, deployment.ID, target)
			if kind == "http" {
				assertHTTPQueryRedactionForViewer(t, f, deployment.ID)
			}
		})
	}
}

func TestVerificationHTTPEncodedPathSeparatorE2E(t *testing.T) {
	f := newVerificationE2E(t)
	target := strings.Replace(encryptedVerificationTarget(t, "http"),
		"/hooks/", "/hooks%2f", 1)
	configureVerification(t, f, "http", target, 5)
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "encoded-path-separator"))
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	assertHTTPQueryRedactionForViewer(t, f, deployment.ID)
}

func TestVerificationHTTPHostnameCredentialE2E(t *testing.T) {
	for _, host := range []string{
		verificationHostCredential, "api.apikey-123456789", "bücher", "xn--bcher-kva",
	} {
		t.Run(host, func(t *testing.T) {
			checkVerificationHostnameRedaction(t, host)
		})
	}
}

func checkVerificationHostnameRedaction(t *testing.T, host string) {
	t.Helper()
	f := newVerificationE2E(t)
	upstream := verificationUpstream(t, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "healthy", r.Host)
			if host == "api.apikey-123456789" {
				fmt.Fprintln(w, "apikey-123456789", "APIKEY-123456789")
			} else if host == verificationHostCredential {
				fmt.Fprintln(
					w,
					host,
					strings.ToUpper(host),
					"StOrEd-HoSt-CrEdEnTiAl",
				)
			} else {
				fmt.Fprintln(
					w,
					"bücher",
					"BÜCHER",
					"xn--bcher-kva",
					"XN--BCHER-KVA",
				)
			}
		}))
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(u.Hostname())
	testdns.Install(t, func(_ string) []net.IP { return []net.IP{ip} })
	u.Host = host + ".hooks.invalid.:" + u.Port()
	u.Path = "/probe"
	configureVerification(t, f, "http", u.String(), 5)
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "hostname-credential"))
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	assertHTTPQueryRedactionForViewer(t, f, deployment.ID)
}

func encryptedVerificationTarget(t *testing.T, kind string) string {
	t.Helper()
	if kind == "bash" {
		return "echo inline-check-proof"
	}
	return verificationUpstream(t, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			value := r.URL.Query().Get("credential")
			if value != verificationQueryCredential ||
				r.URL.Path != "/hooks/"+verificationPathCredential {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprintln(w, "healthy", r.URL.RequestURI())
			fmt.Fprintln(
				w,
				value,
				url.QueryEscape(value),
				url.PathEscape(value),
			)
			fmt.Fprintln(w, verificationPathCredential,
				url.QueryEscape(verificationPathCredential),
				url.PathEscape(verificationPathCredential),
				strings.TrimPrefix(r.URL.EscapedPath(), "/hooks/"))
		})) + "/hooks/" + strings.ReplaceAll(
		url.PathEscape(verificationPathCredential), "%2F", "%2f",
	) + "?credential=" + url.QueryEscape(verificationQueryCredential)
}

func assertHTTPQueryRedactionForViewer(
	t *testing.T, f *artifactE2E, deploymentID int64,
) {
	t.Helper()
	viewer := seedAPIUser(t, f.h.repo, "query-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: viewer.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	_, f.token = seedAPIToken(t, f.h.repo, viewer.ID)
	f.session = "verification-query-viewer"
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: f.session, UserID: viewer.ID, CsrfToken: f.csrf,
			ExpiresAt: 4102444800,
		}); err != nil {
		t.Fatal(err)
	}
	env := string(f.api(t, "GET", fmt.Sprintf(
		"/api/v1/environments/%d", f.environment.ID,
	), nil, 200))
	if strings.Contains(env, "verification_target") {
		t.Fatal("viewer can read the private verification target")
	}
	path := fmt.Sprintf("/deployments/%d", deploymentID)
	var entries []db.DeploymentLog
	if err := json.Unmarshal(f.api(t, "GET", "/api/v1"+path+"/logs", nil, 200),
		&entries); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, entry := range entries {
		lines = append(lines, entry.Line)
	}
	logs := strings.Join(lines, "\n")
	page := html.UnescapeString(f.web(t, "GET", path, nil, 200))
	for _, value := range []string{verificationQueryCredential,
		url.QueryEscape(verificationQueryCredential),
		url.PathEscape(verificationQueryCredential), verificationPathCredential,
		url.QueryEscape(verificationPathCredential),
		url.PathEscape(verificationPathCredential),
		strings.ReplaceAll(url.PathEscape(verificationPathCredential), "%2F", "%2f"),
		verificationHostCredential, strings.ToUpper(verificationHostCredential),
		"StOrEd-HoSt-CrEdEnTiAl", "bücher", "BÜCHER", "xn--bcher-kva", "XN--BCHER-KVA"} {
		if strings.Contains(logs, value) || strings.Contains(page, value) {
			t.Fatal("HTTP response exposed the private target credential")
		}
	}
	for _, suffix := range []string{"key-123456789", "KEY-123456789"} {
		if strings.Contains(logs, suffix) || strings.Contains(page, suffix) {
			t.Fatal(
				"HTTP response exposed a partially redacted hostname credential",
			)
		}
	}
	if !strings.Contains(logs, "healthy") ||
		!strings.Contains(logs, "[REDACTED]") {
		t.Fatal("HTTP response output is missing")
	}
}

func assertEncryptedVerificationSnapshot(
	t *testing.T, f *artifactE2E, deploymentID int64, target string,
) {
	t.Helper()
	env, err := f.h.repo.Queries.GetEnvironment(t.Context(), f.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	check, err := f.h.repo.Queries.GetDeploymentVerification(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if env.VerificationTarget == target || check.Target == target {
		t.Fatal("configuration or frozen target is not encrypted")
	}
	plain, err := f.h.repo.DecryptVerificationTarget(check.Target)
	if err != nil || plain != target {
		t.Fatal("encrypted snapshot cannot recover its target")
	}
	steps, err := f.h.repo.Queries.ListDeploymentSteps(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if strings.Contains(step.ScriptBody, target) {
			t.Fatal("deployment steps contain a plaintext verification copy")
		}
	}
}
