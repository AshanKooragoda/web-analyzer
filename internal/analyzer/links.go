package analyzer

import (
	"net/url"
	"strings"
)

// linkSummary is the result of classifying the links found on a page.
type linkSummary struct {
	internal int
	external int
	// unique holds every distinct absolute http(s) link, without fragment,
	// in the order it first appeared. These are the links that get checked.
	unique []string
}

// classifyLinks resolves hrefs and counts them as internal (same host as the
// page) or external. Links that do not point to a web resource, such as
// mailto:, tel:, javascript: and in-page #fragments, are ignored.
func classifyLinks(pageURL *url.URL, baseHref string, hrefs []string) linkSummary {
	base := pageURL
	if baseHref != "" {
		if b, err := pageURL.Parse(strings.TrimSpace(baseHref)); err == nil {
			base = b
		}
	}

	var s linkSummary
	seen := make(map[string]bool)
	for _, href := range hrefs {
		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "#") {
			continue
		}
		u, err := base.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		u.Fragment, u.RawFragment = "", ""

		if sameSite(u, pageURL) {
			s.internal++
		} else {
			s.external++
		}
		if key := u.String(); !seen[key] {
			seen[key] = true
			s.unique = append(s.unique, key)
		}
	}
	return s
}

// sameSite reports whether two URLs share a host, treating a leading "www."
// as insignificant.
func sameSite(a, b *url.URL) bool {
	return strings.EqualFold(trimWWW(a.Hostname()), trimWWW(b.Hostname()))
}

func trimWWW(host string) string {
	if len(host) > 4 && strings.EqualFold(host[:4], "www.") {
		return host[4:]
	}
	return host
}
