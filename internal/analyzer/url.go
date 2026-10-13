package analyzer

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeURL validates user input and returns an absolute http(s) URL.
// Input without a scheme, such as "example.com", is treated as https.
func NormalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: please enter a URL", ErrInvalidURL)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a valid URL", ErrInvalidURL, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: only http and https URLs are supported, got %q", ErrInvalidURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: %q has no host", ErrInvalidURL, raw)
	}
	return u, nil
}
