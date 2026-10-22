package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"durpdeploy/internal/events"
)

func (d *DiscordNotifier) notifyAttachment(
	ctx context.Context, event events.Event,
) error {
	// Keep all source identifiers in one request, avoiding truncation and
	// message bursts that can exhaust the webhook rate limit.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	payload, err := json.Marshal(map[string]string{
		"content": fmt.Sprintf(
			"DurpDeploy %s. Full details are attached.", event.Type,
		),
	})
	if err != nil {
		return err
	}
	if err := writer.WriteField("payload_json", string(payload)); err != nil {
		return err
	}
	file, err := writer.CreateFormFile(
		"files[0]", "deployment-notification.txt",
	)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, event.Message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, event.DiscordWebhookURL, &body,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	//nolint:errcheck // Delivery status is checked before read-only body cleanup.
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
	}
	return nil
}
