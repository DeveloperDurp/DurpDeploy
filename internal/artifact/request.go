package artifact

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) request(
	ctx context.Context,
	source Repository,
	pin Pin,
) (*http.Response, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pin.URL, nil)
	if err != nil || validURL(req.URL) != nil {
		return nil, ErrInvalid
	}
	origin, err := url.Parse(source.URLTemplate)
	if err != nil || origin.Host != req.URL.Host {
		return nil, ErrInvalid
	}
	switch source.AuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+source.Credential)
	case "basic":
		req.SetBasicAuth(source.Username, source.Credential)
	}
	client := *c.HTTP
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || validURL(next.URL) != nil ||
			next.URL.Host != req.URL.Host {
			return ErrFetch
		}
		return nil
	}
	response, err := client.Do(req)
	// Transport errors can contain URL paths or server-controlled redirect text.
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrFetch
	}
	return response, nil
}
