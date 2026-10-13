package analyzer

import (
	"net/url"
	"slices"
	"testing"
)

func TestClassifyLinks(t *testing.T) {
	pageURL, _ := url.Parse("https://www.example.com/blog/post")

	tests := []struct {
		name         string
		base         string
		hrefs        []string
		wantInternal int
		wantExternal int
		wantUnique   []string
	}{
		{
			name:         "relative and absolute internal",
			hrefs:        []string{"/about", "contact", "../index.html", "https://www.example.com/x", "//www.example.com/y"},
			wantInternal: 5,
			wantUnique: []string{
				"https://www.example.com/about",
				"https://www.example.com/blog/contact",
				"https://www.example.com/index.html",
				"https://www.example.com/x",
				"https://www.example.com/y",
			},
		},
		{
			name:         "www prefix and case ignored",
			hrefs:        []string{"https://example.com/a", "http://EXAMPLE.com/b"},
			wantInternal: 2,
			wantUnique:   []string{"https://example.com/a", "http://EXAMPLE.com/b"},
		},
		{
			name:         "subdomains and other hosts are external",
			hrefs:        []string{"https://blog.example.com/", "https://golang.org", "//cdn.other.net/lib.js"},
			wantExternal: 3,
			wantUnique:   []string{"https://blog.example.com/", "https://golang.org", "https://cdn.other.net/lib.js"},
		},
		{
			name:       "non web links ignored",
			hrefs:      []string{"", "  ", "#top", "mailto:a@b.c", "tel:+123", "javascript:void(0)", "data:text/plain,hi", "http://[::1"},
			wantUnique: nil,
		},
		{
			name:         "duplicates counted but checked once, fragments dropped",
			hrefs:        []string{"/a", "/a#section", "/a", "https://golang.org", "https://golang.org"},
			wantInternal: 3,
			wantExternal: 2,
			wantUnique:   []string{"https://www.example.com/a", "https://golang.org"},
		},
		{
			name:         "base href changes resolution but not ownership",
			base:         "https://cdn.other.net/assets/",
			hrefs:        []string{"img.png", "https://www.example.com/home"},
			wantInternal: 1,
			wantExternal: 1,
			wantUnique:   []string{"https://cdn.other.net/assets/img.png", "https://www.example.com/home"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLinks(pageURL, tt.base, tt.hrefs)
			if got.internal != tt.wantInternal || got.external != tt.wantExternal {
				t.Errorf("internal, external = %d, %d; want %d, %d", got.internal, got.external, tt.wantInternal, tt.wantExternal)
			}
			if !slices.Equal(got.unique, tt.wantUnique) {
				t.Errorf("unique = %q, want %q", got.unique, tt.wantUnique)
			}
		})
	}
}
