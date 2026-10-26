package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func postJSON(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	payload any,
	statusLabel string,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	//nolint:errcheck // Delivery status is checked before read-only body cleanup.
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned status %d", statusLabel, resp.StatusCode)
	}
	return nil
}
