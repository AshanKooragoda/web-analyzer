package analyzer

import (
	"errors"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://example.com", want: "https://example.com"},
		{in: "http://example.com/path?q=1", want: "http://example.com/path?q=1"},
		{in: "  example.com  ", want: "https://example.com"},
		{in: "example.com:8080/x", want: "https://example.com:8080/x"},
		{in: "HTTPS://Example.com", want: "https://Example.com"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "ftp://example.com", wantErr: true},
		{in: "javascript://alert(1)", wantErr: true},
		{in: "https://", wantErr: true},
		{in: "http://exa mple.com", wantErr: true},
		{in: "https://example.com:port", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeURL(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("NormalizeURL(%q) error = %v, want ErrInvalidURL", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeURL(%q) unexpected error: %v", tt.in, err)
			}
			if got.String() != tt.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
