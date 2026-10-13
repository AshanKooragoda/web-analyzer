package analyzer

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const versionUnknown = "Unknown (no DOCTYPE, quirks mode)"

// knownDoctypes maps DOCTYPE public identifiers to HTML versions.
// The identifiers are upper case because matching is case-insensitive.
var knownDoctypes = []struct {
	publicID string
	version  string
}{
	{"-//W3C//DTD XHTML 1.1//", "XHTML 1.1"},
	{"-//W3C//DTD XHTML 1.0 STRICT//", "XHTML 1.0 Strict"},
	{"-//W3C//DTD XHTML 1.0 TRANSITIONAL//", "XHTML 1.0 Transitional"},
	{"-//W3C//DTD XHTML 1.0 FRAMESET//", "XHTML 1.0 Frameset"},
	{"-//W3C//DTD HTML 4.01 TRANSITIONAL//", "HTML 4.01 Transitional"},
	{"-//W3C//DTD HTML 4.01 FRAMESET//", "HTML 4.01 Frameset"},
	{"-//W3C//DTD HTML 4.01//", "HTML 4.01 Strict"},
	{"-//W3C//DTD HTML 4.0 TRANSITIONAL//", "HTML 4.0 Transitional"},
	{"-//W3C//DTD HTML 4.0 FRAMESET//", "HTML 4.0 Frameset"},
	{"-//W3C//DTD HTML 4.0//", "HTML 4.0 Strict"},
	{"-//W3C//DTD HTML 3.2", "HTML 3.2"},
	{"-//IETF//DTD HTML 2.0", "HTML 2.0"},
}

// page holds the facts extracted from a parsed HTML document.
type page struct {
	htmlVersion  string
	title        string
	titleFound   bool
	headings     [6]int // index 0 is h1
	hrefs        []string
	baseHref     string
	hasLoginForm bool
}

// parsePage parses an HTML document and extracts everything the analyzer
// reports in a single walk over the tree.
func parsePage(r io.Reader) (*page, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}
	p := &page{htmlVersion: versionUnknown}
	p.walk(doc, false)
	return p, nil
}

func (p *page) walk(n *html.Node, inForm bool) {
	switch n.Type {
	case html.DoctypeNode:
		p.htmlVersion = detectVersion(n)
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Title:
			// Skip <title> inside inline SVG, which is not the page title.
			if !p.titleFound && n.Namespace == "" {
				p.title = strings.Join(strings.Fields(textContent(n)), " ")
				p.titleFound = true
			}
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			p.headings[n.Data[1]-'1']++
		case atom.A:
			if href, ok := attr(n, "href"); ok {
				p.hrefs = append(p.hrefs, href)
			}
		case atom.Base:
			if href, ok := attr(n, "href"); ok && p.baseHref == "" {
				p.baseHref = href
			}
		case atom.Form:
			inForm = true
		case atom.Input:
			if typ, _ := attr(n, "type"); inForm && strings.EqualFold(typ, "password") {
				p.hasLoginForm = true
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		p.walk(c, inForm)
	}
}

// detectVersion derives the HTML version from a DOCTYPE node.
func detectVersion(n *html.Node) string {
	if !strings.EqualFold(n.Data, "html") {
		return "Unknown (" + n.Data + ")"
	}
	public, _ := attr(n, "public")
	system, _ := attr(n, "system")

	if public == "" && (system == "" || strings.EqualFold(system, "about:legacy-compat")) {
		return "HTML5"
	}
	upper := strings.ToUpper(public)
	for _, d := range knownDoctypes {
		if strings.Contains(upper, d.publicID) {
			return d.version
		}
	}
	return "Unknown (" + public + ")"
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return sb.String()
}
