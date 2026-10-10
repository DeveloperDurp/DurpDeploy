package artifact

// FetchError reports a safe reason without transport URLs or credentials.
type FetchError struct{ reason string }

func (e *FetchError) Error() string {
	return ErrFetch.Error() + ": " + e.reason
}

func (e *FetchError) Unwrap() error { return ErrFetch }
