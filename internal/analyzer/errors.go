package analyzer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ErrInvalidURL is returned when the user supplied URL cannot be analyzed.
var ErrInvalidURL = errors.New("invalid URL")

// FetchError reports that the target page could not be retrieved.
// StatusCode is either the status returned by the target server or, for
// network level failures, the gateway status that best describes the problem.
type FetchError struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *FetchError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %s: %v", e.StatusCode, http.StatusText(e.StatusCode), e.Message, e.Err)
	}
	return fmt.Sprintf("%d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Message)
}

func (e *FetchError) Unwrap() error { return e.Err }

// transportError converts an error returned by an HTTP client into a
// FetchError with a description a user can act on.
func transportError(target *url.URL, timeout time.Duration, err error) *FetchError {
	var dnsErr *net.DNSError
	var netErr net.Error

	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return &FetchError{
			StatusCode: http.StatusGatewayTimeout,
			Message:    fmt.Sprintf("%s did not respond within %s", target.Host, timeout),
			Err:        err,
		}
	case errors.As(err, &dnsErr):
		return &FetchError{
			StatusCode: http.StatusBadGateway,
			Message:    fmt.Sprintf("the host %q could not be resolved; check the address for typos", target.Hostname()),
			Err:        err,
		}
	default:
		return &FetchError{
			StatusCode: http.StatusBadGateway,
			Message:    fmt.Sprintf("could not connect to %s", target.Host),
			Err:        err,
		}
	}
}
