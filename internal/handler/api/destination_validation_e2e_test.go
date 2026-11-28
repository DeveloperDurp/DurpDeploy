//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestNotificationDestinationValidationAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for _, scope := range []struct{ api, web string }{
		{f.base() + "/notifications",
			fmt.Sprintf("/projects/%d/notifications", f.project.ID)},
		{"/api/v1/admin/notifications/settings", "/admin/notifications/settings"},
	} {
		for _, web := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/web=%v", scope.api, web), func(t *testing.T) {
				checkNotificationDestinations(t, f, scope.api, scope.web, web)
			})
		}
	}
}

func checkNotificationDestinations(
	t *testing.T,
	f *artifactE2E,
	api, webPath string,
	web bool,
) {
	t.Helper()
	update := func(field, value string, status int) {
		t.Helper()
		if web {
			f.web(
				t,
				"POST",
				webPath,
				url.Values{field: {value}},
				status,
			)
		} else {
			f.api(t, "PUT", api, map[string]string{field: value}, status)
		}
	}
	accepted, rejectedURL := 200, 400
	if web {
		accepted, rejectedURL = 303, 422
	}
	update(
		"notify_emails",
		` a@example.com, "ops team"@example.com, `,
		accepted,
	)
	saved := readSettings(t, f, api)
	for _, field := range []string{
		"slack_webhook_url", "gotify_url", "discord_webhook_url",
	} {
		for _, host := range rejectedDestinationHosts() {
			update(field, "http://"+host+"/hook", rejectedURL)
			assertSettingsUnchanged(t, f, api, saved)
		}
	}
	for _, email := range []string{
		"a@example.com\r\nBcc: b@example.com", "a@example.com\n",
		"\ra@example.com", "\ta@example.com", "a@example.com\x00",
		"a@example.com\x7f", "a@example.com\u0085", "invalid",
		"Name <a@example.com>",
		"ops:a@example.com;", "a@ example.com",
	} {
		update("notify_emails", email, 422)
		assertSettingsUnchanged(t, f, api, saved)
	}
	update(
		"gotify_url",
		"https://2130706433.example/hook",
		accepted,
	)
}

func TestPackageDestinationValidationAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	api := f.base() + "/package-repository"
	for _, web := range []bool{false, true} {
		for _, host := range []string{
			"10.0.0.1", "host.containers.internal", "REPO.EXAMPLE.",
			"2130706433.example", "[fd00::1]", "100.100.100.199",
		} {
			updatePackageDestination(t, f, web, host, true)
			saved := readSettings(t, f, api)
			if saved["url_template"] != "https://"+host+"/{version}.zip" {
				t.Fatal("save rewrote source identity")
			}
		}
		saved := readSettings(t, f, api)
		for _, host := range append(rejectedDestinationHosts(),
			"100.100.100.200", "[fd00:ec2::254]") {
			if strings.EqualFold(
				strings.TrimSuffix(host, "."),
				"host.containers.internal",
			) {
				continue
			}
			updatePackageDestination(t, f, web, host, false)
			assertSettingsUnchanged(t, f, api, saved)
		}
	}
}

func updatePackageDestination(
	t *testing.T,
	f *artifactE2E,
	web bool,
	host string,
	accepted bool,
) {
	t.Helper()
	value := "https://" + host + "/{version}.zip"
	status := 422
	if web {
		if accepted {
			status = 303
		}
		path := fmt.Sprintf("/projects/%d/package-repository", f.project.ID)
		f.web(t, "POST", path, url.Values{
			"url_template": {value}, "auth_type": {"noauth"},
		}, status)
		return
	}
	if accepted {
		status = 200
	}
	f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": value, "auth_type": "noauth",
	}, status)
}

func rejectedDestinationHosts() []string {
	return []string{
		"127.0.0.1", "127.0.0.1.", "LOCALHOST.", "service.localhost.",
		"2130706433", "0x7f000001", "0X7F.0.0.1", "0177.0.0.1",
		"127.1", "127.0.1", "127.000.0.1", "0x7f.0.0.1.",
		"2852039166", "0xa9.0xfe.0xa9.0xfe", "0251.0376.0251.0376",
		"169.254.169.254", "[::ffff:127.0.0.1]", "[fe80::1%25eth0]",
		"host.containers.internal", "HOST.CONTAINERS.INTERNAL.",
	}
}

func readSettings(t *testing.T, f *artifactE2E, path string) map[string]any {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func assertSettingsUnchanged(
	t *testing.T,
	f *artifactE2E,
	path string,
	saved map[string]any,
) {
	t.Helper()
	if !reflect.DeepEqual(readSettings(t, f, path), saved) {
		t.Fatal("rejected update changed saved settings")
	}
}
