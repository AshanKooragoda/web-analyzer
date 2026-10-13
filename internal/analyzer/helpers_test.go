package analyzer

import (
	"io"
	"net/http"
	"strings"
)

// doerFunc adapts a function to HTTPDoer so tests never touch the network.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func response(req *http.Request, status int, contentType, body string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func htmlResponse(req *http.Request, body string) *http.Response {
	return response(req, http.StatusOK, "text/html; charset=utf-8", body)
}
