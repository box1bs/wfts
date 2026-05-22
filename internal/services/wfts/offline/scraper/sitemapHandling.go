package scraper

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"wfts/internal/model"
	"wfts/internal/utils/parser"

	"golang.org/x/net/html/charset"
)

const (
	BaseXMLPageError = "sitemap page"
	sitemap = "sitemap"
)

func resetUrl(uri *url.URL) string {
	var sb strings.Builder
	sb.WriteString(uri.Scheme)
	sb.WriteString("://")
	sb.WriteString(uri.Hostname())
	sb.WriteByte('/')
	return sb.String()
}

func (ws *WebScraper) fetchPageRulesAndOffers(ctx context.Context, cur *url.URL) ([]*model.LinkToken, *parser.RobotsTxt, error) {
	robotsTXT := &parser.RobotsTxt{}
	if r, err := parser.FetchRobotsTxt(ctx, resetUrl(cur), ws.client); r != "" && err == nil {
		*robotsTXT = *parser.ParseRobotsTxt(r)
		ws.mu.Lock()
		if lim, ok := ws.rlCache.Get(cur.Hostname()).(*rateLimiter); ok && (lim == nil || lim.R == DefaultDelay) && robotsTXT.Rules["*"].Delay > 0 {
			ws.rlCache.Put(cur.Hostname(), NewRateLimiter(robotsTXT.Rules["*"].Delay))
		}
		ws.mu.Unlock()
	} else {
		robotsTXT = nil
	}

	links, err := ws.prepareSitemapLinks(ctx, cur)
	return links, robotsTXT, err
}

func (ws *WebScraper) haveSitemap(url *url.URL) ([]string, error) {
	sitemapURL := resetUrl(url)
	if !strings.Contains(sitemapURL, sitemap) {
		sitemapURL = strings.TrimSuffix(url.String(), "/")
		sitemapURL = sitemapURL + "/" + sitemap + ".xml"
	}

	urls, err := ws.processSitemap(url, sitemapURL)
	if err != nil {
		return nil, err
	}

	return urls, err
}

func decodeSitemap(r io.Reader) ([]string, error) {
	var urls []string
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if element, ok := token.(xml.StartElement); ok {
			if element.Name.Local == "loc" {
				var url string
				if err := dec.DecodeElement(&url, &element); err != nil {
					continue
				}
				urls = append(urls, url)
			}
		}
	}

	return urls, nil
}

func (ws *WebScraper) processSitemap(baseURL *url.URL, sitemapURL string) ([]string, error) {
	sitemap, err := getSitemapURLs(sitemapURL, ws.client)
	if err != nil {
		return nil, err
	}

	var nextUrls []string
	for _, item := range sitemap {
		abs, err := makeAbsoluteURL(item, baseURL)
		if abs == "" || err != nil {
			continue
		}
		nextUrls = append(nextUrls, abs)
	}

	return nextUrls, nil
}

func getSitemapURLs(URL string, cli *http.Client) ([]string, error) {
	resp, err := cli.Get(URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimPrefix(bytes.ReplaceAll(body, []byte(`xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`), []byte("")), []byte("\xef\xbb\xbf"))
	return decodeSitemap(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
}

func (ws *WebScraper) prepareSitemapLinks(ctx context.Context, current *url.URL) ([]*model.LinkToken, error) {
	links := make([]*model.LinkToken, 0)
	var urls []string
	var err error
	log := ctx.Value(model.DefLogKey).(*model.Logger)
	if log == nil {
		return nil, fmt.Errorf(canceled)
	}
	if urls, err = ws.haveSitemap(current); err == nil && len(urls) > 0 {
		for _, link := range urls {
			parsed, err := url.Parse(link)
			if err != nil {
				log.Errorf("error parsing link %s: %v", link, err)
				continue
			}
			same := isSameOrigin(parsed, current)
			if !same && ws.cfg.OnlySameDomain {
				continue
			}
			links = append(links, &model.LinkToken{Link: parsed, SameDomain: same})
		}
	}
	if err == nil {
		err = errors.New(BaseXMLPageError)
	}
	return links, err
}