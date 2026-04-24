package scraper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"wfts/internal/model"
	"wfts/internal/utils/parser"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

type linkToken struct {
	Link 		*url.URL
	Priority 	float64
	Depth 		int
	// Ancore		string
	SameDomain 	bool
}

func (ws *WebScraper) fetchHTMLcontent(ctx context.Context, pr *float64, cur *url.URL, norm string, gd int) ([]*linkToken, error) {
	ws.mu.Lock()
	rl, ok := ws.rlCache.Get(cur.Hostname()).(*rateLimiter)
	if !ok || rl == nil {
		rl = NewRateLimiter(DefaultDelay)
		ws.rlCache.Put(cur.Hostname(), rl)
	}
	ws.mu.Unlock()
	doc, err := ws.getHTML(ctx, cur.String(), rl, numOfTries)
	log := ctx.Value(model.DefLogKey).(*model.Logger)
	if log == nil {
		return nil, fmt.Errorf(canceled)
	}
    if err != nil {
		log.Errorf("url page error: %v", err)
        return nil, err
    }
	if doc == "" {
		log.Debugf("empty html content")
        return nil, fmt.Errorf("empty html content on page: %s", cur)
	}
	
	hashed := sha256.Sum256([]byte(norm))
    document := &model.Document{
        Id:  hashed,
        URL: cur.String(),
    }

	features := &model.CrawlFeatures{PathLen: len(cur.Path), UrlLen: len(cur.String()), HostLen: len(cur.Hostname())}
	c, cancel := context.WithTimeout(ctx, deadlineTime)
	defer cancel()
    links, passages := ws.parseHTMLStream(c, doc, cur, features, gd)
	if l := len(links); l != 0 {
		ws.lru.Put(hashed, links)
		*pr += math.Log(float64(l) + 1)
	}

	return links, ws.HandleDocumentWords(ctx, document, features, pr, passages)
}

func (ws *WebScraper) parseHTMLStream(ctx context.Context, htmlContent string, baseURL *url.URL, features *model.CrawlFeatures, currentDeep int) (links []*linkToken, pasages []model.Passage) {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var headerType byte
	var garbageTagCounter int
	// var isAncore bool
	links = make([]*linkToken, 0, 64)
	visit := make([]*linkToken, 0, 16)
	curDomDepth := 0

	rules, ok := ws.rulesCache.Get(baseURL.Hostname()).(*parser.RobotsTxt)
	if !ok {
		rules = nil
	}

	log := ctx.Value(model.DefLogKey).(*model.Logger)

	tokenCount := 0
	const checkContextEvery = 128

	for {
		tokenCount++
		if tokenCount % checkContextEvery == 0 {
			select {
			case <-ctx.Done():
				if len(visit) != 0 {
					links = append(links, visit...)
				}
				return
			default:
			}
		}

		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				break
			}
			log.Errorf("error parsing HTML")
			break
		}

		switch tokenType {
		case html.StartTagToken:
			curDomDepth++
			features.TagCount++
			if garbageTagCounter > 0 {
				break
			}

			t := tokenizer.Token()
			tagName := t.Data
			switch tagName {
			case "html":
				for _, attr := range t.Attr {
					if attr.Key == "lang" && !strings.Contains(strings.ToLower(attr.Val), "en") {return}
				}

			case "h1", "h2":
				headerType += tagName[1]

			case "div":
				if garbageTagCounter > 0 {
					garbageTagCounter++
					break
				}
				for _, attr := range t.Attr {
					if attr.Key == "class" || attr.Key == "id" {
						val := attr.Val
						if strings.Contains(val, "ad") || strings.Contains(val, "banner") || strings.Contains(val, "promo") {
							garbageTagCounter++
							break
						}
					}
				}

			case "a":
				for _, attr := range t.Attr {
					if attr.Key == "href" {
						link, err := makeAbsoluteURL(attr.Val, baseURL)
						if err != nil {
							break
						}
						if link != "" {
							uri, err := url.Parse(link)
							if err != nil || uri == nil {
								log.Errorf("error parsing link: %v", err)
								break
							}
							normalized, err := normalizeUrl(uri)
							if err != nil {
								log.Errorf("error normalizing url: %v", err)
								break
							}
							if ext := strings.ToLower(path.Ext(uri.Path)); ext == ".pdf" || ext == ".xml" {
								break
							}
							if types := uri.Query()["format"]; len(types) > 0 {
								var allowed bool
								for _, t := range types {
									if t == "html" {
										allowed = true
										break
									}
								}

								if !allowed {
									break
								}
							}
							if rules != nil {
								if !rules.IsAllowed(userAgent, uri.Path)  || !rules.IsAllowed("*", uri.Path){
									break
								}
							}
							// isAncore = true
							same := isSameOrigin(uri, baseURL)
							if depth, vis := ws.visited.Load(normalized); vis {
								if depth.(int) > currentDeep {
									features.UrlCount++
									visit = append(visit, &linkToken{Link: uri, SameDomain: same})
								}
								break
							}
							features.UrlCount++
							links = append(links, &linkToken{Link: uri, SameDomain: same})
						}
						break
					}
				}

			case "script", "style", "iframe", "aside", "nav", "footer":
				garbageTagCounter++

			}

		case html.EndTagToken:
			if curDomDepth > features.DomDepth {features.DomDepth = curDomDepth}
			curDomDepth--
			t := tokenizer.Token()
			tagName := strings.ToLower(t.Data)
			// if isAncore && tagName[0] == 'a' {
			// 	isAncore = false
			// 	continue
			// }
			if tagName[0] == 'h' && len(tagName) > 1 && (tagName[1] == '1' || tagName[1] == '2') {
				headerType -= tagName[1]
				continue
			}

			if garbageTagCounter > 0 && isGarbage(tagName) {
				garbageTagCounter--
			}

		case html.TextToken:
			// if isAncore && len(links) > 0 {
			// 	links[len(links) - 1].Ancore = string(bytes.TrimSpace(tokenizer.Text()))
			// 	continue
			// }
			if garbageTagCounter > 0 {
				continue
			}

			if headerType > 0 {
				b := bytes.TrimSpace(tokenizer.Text())
				if len(b) == 0 {
    				continue
				}
				pasages = append(pasages, model.NewTypeTextObj[model.Passage](model.HeaderType, string(b), 0))
				continue
			}

			b := bytes.TrimSpace(tokenizer.Text())
			if len(b) == 0 {
    			continue
			}
			pasages = append(pasages, model.NewTypeTextObj[model.Passage](model.BodyType, string(b), 0))

		}
	}
	if curDomDepth > features.DomDepth {features.DomDepth = curDomDepth}
	if len(visit) != 0 {
		links = append(links, visit...)
	}
	return
}

var garbageTags = []string{"script", "style", "iframe", "aside", "nav", "footer", "div"}
func isGarbage(tag string) bool {
	return slices.Contains(garbageTags, tag)
}

const wantedCharset = "utf-8"
var metaCharsetRe = regexp.MustCompile(`(?i)<meta\s+[^>]*charset\s*['"]([^'"]+)['"]`)

func (ws *WebScraper) getHTML(ctx context.Context, URL string, rl *rateLimiter, try int) (string, error) {
	if try <= 0 {
		return "", fmt.Errorf("http status code: %d, and max amount of tries was reached", http.StatusTooManyRequests)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", URL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")

	rl.GetToken(ws.globalCtx) // не должно ложить приложение, но в целом по желанию
	resp, err := ws.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests && !ws.checkContext(ctx) {
		<-time.After(deadlineTime)
		return ws.getHTML(ctx, URL, rl, try - 1)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	if ws.checkContext(ws.globalCtx) {
		return "", context.Canceled
	}

	ctype := resp.Header.Get("Content-Type")
	if !strings.Contains(ctype, "text/html") {
		return "", fmt.Errorf("content-type: %s", ctype)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
    if err != nil {
        return "", err
    }

	if ws.checkContext(ctx) {return "", context.Canceled}

	htmlText := string(body)
	chset := charsetFromResponse(req.Header, htmlText)
	if chset != wantedCharset {
		htmlText = convertCharset(chset, body)
	}
	return htmlText, nil
}

func charsetFromResponse(header http.Header, html string) string {
    if cset := strings.ToLower(header.Get("charset")); cset != "" {
        return cset
    }
    if match := metaCharsetRe.FindStringSubmatch(html); len(match) > 1 {
        return strings.ToLower(strings.TrimSpace(match[1]))
    }
    return wantedCharset
}

func convertCharset(chset string, data []byte) string {
	enc, _ := charset.Lookup(chset)
	if enc == nil {
		enc = encoding.Nop
	}
	utf8Bytes, err := io.ReadAll(enc.NewDecoder().Reader(bytes.NewReader(data)))
	if err != nil {
		return string(data)
	}
	return string(utf8Bytes)
}