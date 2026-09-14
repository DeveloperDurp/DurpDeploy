package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestNotificationsPageUsesAlpineDialogTrigger(t *testing.T) {
	// Given
	var output bytes.Buffer
	entries := []db.ListNotificationEventsRow{{ID: 42}}

	// When
	err := NotificationsPage(entries, "/admin/notifications", nil).
		Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render notifications page: %v", err)
	}
	rendered := output.String()

	// Then
	if !strings.Contains(rendered, "x-data") {
		t.Fatal("notifications page is missing a local Alpine scope")
	}
	if !strings.Contains(
		rendered,
		`x-data="{}"`,
	) {
		t.Fatal("notifications page does not own a local Alpine scope")
	}
	if !strings.Contains(rendered, `x-on:click="$refs[$el.dataset.modalTarget].showModal()"`) {
		t.Fatal("notification row does not open its local Alpine dialog ref")
	}
	if !strings.Contains(
		rendered,
		`<dialog id="notification_modal_42" x-ref="notification_modal_42" class="modal">`,
	) {
		t.Fatal("notification dialog does not expose its existing ID as an Alpine ref")
	}
	if strings.Contains(rendered, "onclick=") {
		t.Fatal("notifications page still renders an ordinary onclick handler")
	}
	if strings.Contains(rendered, "document.getElementById") {
		t.Fatal("notifications page still performs a global ID lookup")
	}
}

func TestNotificationsPageKeepsDeliveryStatusesReadable(t *testing.T) {
	// Given
	var output bytes.Buffer
	entries := []db.ListNotificationEventsRow{{
		ID:      42,
		Message: "Deployment completed",
		Results: `{"email":"ok","slack":"ok"}`,
	}}

	// When
	err := NotificationsPage(entries, "/admin/notifications", nil).
		Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render notifications page: %v", err)
	}
	rendered := output.String()

	// Then
	for _, marker := range []string{
		`class="btn btn-outline btn-sm">Settings</a>`,
		`class="hidden xl:table table-zebra table-fixed w-full"`,
		`data-mobile-notification-list`,
		`class="space-y-3 xl:hidden"`,
		`class="flex flex-wrap gap-1"`,
	} {
		if !strings.Contains(rendered, marker) {
			t.Errorf("responsive notification layout missing %q", marker)
		}
	}
}
