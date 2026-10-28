//go:build e2e && transportsecurity && forwardingsecurity

package api_test

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"
)

func TestTransportForwardingPeerE2E(t *testing.T) {
	f := loadTransportFixture(t)
	if f.ForwardURL == "" || f.ExpectedIP == "" || f.ForwardMode == "" {
		t.Fatal("forwarding peer fixture is incomplete")
	}
	original := f.URL
	f.URL = f.ForwardURL
	client := f.client(t)
	form := url.Values{"email": {f.Email}, "password": {f.Password}}
	req := transportRequest(t, "POST", f.URL+"/login", form.Encode())
	marker := "transport-forwarding-" + f.ForwardMode
	req.Header.Set("User-Agent", marker)
	req.Header.Set("X-Forwarded-For", "203.0.113.90")
	req.Header.Set("X-Forwarded-Proto", "http")
	res, _ := transportResponse(t, client, req, 303)
	secure := false
	for _, c := range res.Cookies() {
		if c.Name == "session" {
			secure = c.Secure && c.HttpOnly
		}
	}
	if !secure {
		t.Fatal("forwarding peer changed configured cookie security")
	}
	req = transportRequest(t, "GET",
		original+"/api/v1/admin/audit?action=login", "")
	req.Header.Set("Authorization", "Bearer "+f.Token)
	_, body := transportResponse(t, client, req, 200)
	var records struct {
		Items []struct{ Details sql.NullString }
	}
	if json.Unmarshal(body, &records) != nil {
		t.Fatal("forwarding audit response was invalid")
	}
	for _, record := range records.Items {
		var details struct {
			IP        string `json:"ip"`
			UserAgent string `json:"user_agent"`
		}
		if !record.Details.Valid ||
			json.Unmarshal([]byte(record.Details.String), &details) != nil {
			t.Fatal("forwarding audit record was invalid")
		}
		if details.UserAgent == marker {
			if details.IP != f.ExpectedIP {
				t.Fatal(
					"proxy chain did not select the canonical client address",
				)
			}
			return
		}
	}
	t.Fatal("public audit API omitted the forwarding test login")
}
