package analyzer

import (
	"slices"
	"strings"
	"testing"
)

func TestDetectVersion(t *testing.T) {
	tests := []struct {
		name    string
		doctype string
		want    string
	}{
		{"html5", `<!DOCTYPE html>`, "HTML5"},
		{"html5 lower case", `<!doctype html>`, "HTML5"},
		{"html5 legacy compat", `<!DOCTYPE html SYSTEM "about:legacy-compat">`, "HTML5"},
		{"html 4.01 strict", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">`, "HTML 4.01 Strict"},
		{"html 4.01 transitional", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN" "http://www.w3.org/TR/html4/loose.dtd">`, "HTML 4.01 Transitional"},
		{"html 4.01 frameset", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Frameset//EN" "http://www.w3.org/TR/html4/frameset.dtd">`, "HTML 4.01 Frameset"},
		{"xhtml 1.0 strict", `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">`, "XHTML 1.0 Strict"},
		{"xhtml 1.0 transitional", `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">`, "XHTML 1.0 Transitional"},
		{"xhtml 1.1", `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">`, "XHTML 1.1"},
		{"html 3.2", `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 3.2 Final//EN">`, "HTML 3.2"},
		{"no doctype", ``, versionUnknown},
		{"unknown public id", `<!DOCTYPE html PUBLIC "-//ACME//DTD Custom//EN">`, "Unknown (-//ACME//DTD Custom//EN)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parsePage(strings.NewReader(tt.doctype + `<html><head><title>x</title></head><body></body></html>`))
			if err != nil {
				t.Fatal(err)
			}
			if p.htmlVersion != tt.want {
				t.Errorf("htmlVersion = %q, want %q", p.htmlVersion, tt.want)
			}
		})
	}
}

func TestParsePageTitle(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"simple", `<title>Hello</title>`, "Hello"},
		{"whitespace collapsed", "<title>\n  Hello\t  World \n</title>", "Hello World"},
		{"entities decoded", `<title>Fish &amp; Chips</title>`, "Fish & Chips"},
		{"missing", `<p>no title</p>`, ""},
		{"svg title ignored", `<body><svg><title>icon</title></svg></body>`, ""},
		{"first title wins", `<title>One</title><title>Two</title>`, "One"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parsePage(strings.NewReader(tt.html))
			if err != nil {
				t.Fatal(err)
			}
			if p.title != tt.want {
				t.Errorf("title = %q, want %q", p.title, tt.want)
			}
		})
	}
}

func TestParsePageHeadings(t *testing.T) {
	doc := `<h1>a</h1><h2>b</h2><h2>c</h2><div><h3>d</h3><section><h3>e</h3><h3>f</h3></section></div><h6>g</h6>`
	p, err := parsePage(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := [6]int{1, 2, 3, 0, 0, 1}
	if p.headings != want {
		t.Errorf("headings = %v, want %v", p.headings, want)
	}
}

func TestParsePageLoginForm(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{"login form", `<form><input name="user"><input type="password" name="pw"></form>`, true},
		{"type is case insensitive", `<form><input type="PASSWORD"></form>`, true},
		{"nested password field", `<form><div><label>Pw <input type="password"></label></div></form>`, true},
		{"search form", `<form><input type="search" name="q"></form>`, false},
		{"password outside form", `<input type="password"><form><input type="text"></form>`, false},
		{"no form", `<p>hello</p>`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parsePage(strings.NewReader(tt.html))
			if err != nil {
				t.Fatal(err)
			}
			if p.hasLoginForm != tt.want {
				t.Errorf("hasLoginForm = %v, want %v", p.hasLoginForm, tt.want)
			}
		})
	}
}

func TestParsePageLinks(t *testing.T) {
	doc := `<html><head><base href="https://cdn.example.com/"></head><body>
		<a href="/a">a</a><a>no href</a><a href="https://other.org">b</a><a href="">empty</a>
		<base href="https://ignored.example.com/">
	</body></html>`
	p, err := parsePage(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/a", "https://other.org", ""}; !slices.Equal(p.hrefs, want) {
		t.Errorf("hrefs = %q, want %q", p.hrefs, want)
	}
	if p.baseHref != "https://cdn.example.com/" {
		t.Errorf("baseHref = %q, want first <base>", p.baseHref)
	}
}
