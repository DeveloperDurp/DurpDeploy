//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestUsersFormAccessibilityAPIWebE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	assertUserFormLabels(t, f.request(t, "GET", "/admin/users/new", "",
		"", f.session, false, 200), true)
	assertUserFormFragment(t, f, "/admin/users/new", true)
	body := url.Values{
		"email": {"accessible-user@example.test"},
		"name":  {`Accessible <user> & name`}, "role": {"maintainer"},
		"password": {
			"Form-access-pass-1",
		}, "password_confirmation": {"mismatch"},
	}
	for _, hx := range []bool{false, true} {
		page := f.request(t, "POST", "/admin/users", body.Encode(),
			"", f.session, hx, 422)
		assertUserFormRejection(t, page, body, true, "Passwords do not match")
	}
	f.request(
		t,
		"POST",
		"/api/v1/admin/users",
		`{"email":"accessible-user@example.test","name":"API invalid","role":"invalid","password":"Form-access-pass-1"}`,
		f.token,
		"",
		false,
		400,
	)
	for _, user := range f.listedUsers(t) {
		if user.Email == body.Get("email") {
			t.Fatal("rejected form persisted a user")
		}
	}
	body.Set("password_confirmation", body.Get("password"))
	f.request(t, "POST", "/admin/users", body.Encode(), "",
		f.session, false, 200)
	user := userWithRole(t, f, body.Get("email"), "maintainer")
	for _, hx := range []bool{false, true} {
		page := f.request(t, "POST", "/admin/users", body.Encode(),
			"", f.session, hx, 422)
		assertUserFormRejection(t, page, body, true,
			"A user with that email already exists")
	}
	path := fmt.Sprintf("/admin/users/%d", user.ID)
	assertUserFormLabels(t, f.request(t, "GET", path+"/edit", "",
		"", f.session, false, 200), false)
	assertUserFormFragment(t, f, path+"/edit", false)
	body.Set("name", "")
	body.Set("password_confirmation", "mismatch")
	for _, hx := range []bool{false, true} {
		page := f.request(t, "PUT", path, body.Encode(), "",
			f.session, hx, 422)
		assertUserFormRejection(t, page, body, false, "Name is required")
	}
	if saved := userWithRole(t, f, user.Email, "maintainer"); saved.Name != user.Name {
		t.Fatal("rejected edit changed the account")
	}
}

func assertUserFormFragment(
	t *testing.T,
	f usersPhoneFixture,
	path string,
	isNew bool,
) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET",
		f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	req.Header.Set("HX-Request", "true")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	fragment, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("edit fragment: status %d, read error %v",
			response.StatusCode, err)
	}
	assertUserFormLabels(t, fragment, isNew)
	if strings.Contains(string(fragment), "<html") ||
		!strings.Contains(string(fragment), "data-user-form") {
		t.Fatal("modal did not return a form fragment")
	}
}

func userFormAttribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func assertUserFormLabels(t *testing.T, page []byte, isNew bool) {
	t.Helper()
	document := dashboardDocument(t, string(page))
	names := []string{"name", "role", "password", "password_confirmation"}
	if isNew {
		names = append(names, "email")
	}
	for _, name := range names {
		fields := dashboardElements(document, "name", name)
		if len(fields) != 1 {
			t.Fatalf("expected one field %s", name)
		}
		id := userFormAttribute(fields[0], "id")
		labels := dashboardElements(document, "for", id)
		if id == "" || len(labels) != 1 ||
			strings.TrimSpace(dashboardText(labels[0])) == "" {
			t.Fatalf("field %s has no associated label", name)
		}
		for _, description := range strings.Fields(
			userFormAttribute(fields[0], "aria-describedby"),
		) {
			messages := dashboardElements(document, "id", description)
			if len(messages) != 1 ||
				strings.TrimSpace(dashboardText(messages[0])) == "" {
				t.Fatalf(
					"field %s has a missing description %s",
					name,
					description,
				)
			}
		}
	}
}

func assertUserFormRejection(
	t *testing.T,
	page []byte,
	values url.Values,
	isNew bool,
	message string,
) {
	t.Helper()
	assertUserFormLabels(t, page, isNew)
	document := dashboardDocument(t, string(page))
	errors := dashboardElements(document, "id", "form-error")
	if len(errors) != 1 || userFormAttribute(errors[0], "role") != "alert" ||
		userFormAttribute(errors[0], "tabindex") != "-1" ||
		!strings.Contains(dashboardText(errors[0]), message) {
		t.Fatal("validation response lost its accessible error summary")
	}
	for _, name := range []string{"email", "name"} {
		if name == "email" && !isNew {
			continue
		}
		field := dashboardElements(document, "name", name)[0]
		if userFormAttribute(field, "value") != values.Get(name) {
			t.Fatalf("validation lost %s", name)
		}
	}
	assertUserFormRole(t, page, values.Get("role"))
	for _, name := range []string{"password", "password_confirmation"} {
		field := dashboardElements(document, "name", name)[0]
		if userFormAttribute(field, "value") != "" {
			t.Fatal("validation response retained a password")
		}
	}
}
