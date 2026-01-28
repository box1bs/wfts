package scraper

import (
	"errors"
	"net/url"
	"strings"
)

func makeAbsoluteURL(rawURL string, baseURL *url.URL) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("empty url")
	}

	if strings.HasPrefix(rawURL, "#") || strings.HasPrefix(strings.ToLower(rawURL), "javascript:") {
		return "", errors.New("ignored url type")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	resolved := baseURL.ResolveReference(u)
	resolved.Fragment = ""
	if resolved.Scheme != "https" {
		return "", errors.New("invalid protocol scheme: " + resolved.Scheme)
	}
	return resolved.String(), nil
}

func normalizeUrl(uri *url.URL) (string, error) {
	host := strings.TrimPrefix(strings.ToLower(truncatePort(uri)), "www.")

	path := uri.Path
	if strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	path = strings.TrimSuffix(path, "/")

	query := uri.Query().Encode()
	var sb strings.Builder
	sb.WriteString(host)
	sb.WriteString(path)
	if query != "" {
		sb.WriteString("?")
		sb.WriteString(query)
	}
	return sb.String(), nil
}

func truncatePort(uri *url.URL) string {
	return strings.Split(uri.Hostname(), ":")[0]
}

func isSameOrigin(rawURL *url.URL, baseURL *url.URL) bool {
	return strings.Contains(truncatePort(baseURL), truncatePort(rawURL))
}