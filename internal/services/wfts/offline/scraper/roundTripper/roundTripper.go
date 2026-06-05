package roundTripper

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

var ErrBlocked = errors.New("Blocked by filter")

type RoundTripper struct {
	base   http.RoundTripper
	filter *Bloom
}

func SetUpRoundTripper(filterUrl string, transport *http.Transport) (*RoundTripper, error) {
	rt := &RoundTripper{
		base: 	transport,
		filter: &Bloom{},
	}
	if err := rt.uploadTXTFilter(filterUrl); err != nil {
		return nil, err
	}
	return rt, nil
}

func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if rt.isBlocked(host) {
		return nil, ErrBlocked
	}
	return rt.base.RoundTrip(req)
}

func (rt *RoundTripper) isBlocked(host string) bool {
	parts := strings.Split(host, ".")
    for i := range parts {
        candidate := strings.Join(parts[i:], ".")
        if rt.filter.Contain(candidate) {
            return true
        }
    }
    return false
}

func (rt *RoundTripper) uploadTXTFilter(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "@@") {
			continue
		}

		if strings.HasPrefix(trimmed, "||") && strings.HasSuffix(trimmed, "^") {
			rt.filter.Add(trimmed[2 : len(trimmed)-1])
		}
	}
	return nil
}