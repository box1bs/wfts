package scraper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"wfts/internal/model"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

type linkToken struct {
	Link 		*url.URL
	Ancore		string // пока просто собираем
	SameDomain 	bool
}

func (ws *WebScraper) fetchHTMLcontent(ctx context.Context, pr *float64, cur *url.URL, norm string, gd int) ([]*linkToken, error) {
	ws.rlMu.RLock()
	rl := ws.rlMap[cur.Host]
	ws.rlMu.RUnlock()
	doc, err := ws.getHTML(ctx, cur.String(), rl, numOfTries)
	log := ctx.Value(model.DefLogKey).(*model.Logger)
	if log == nil {
		return nil, fmt.Errorf("context canceled")
	}
    if err != nil {
		log.Errorf("error getting html: %v", err)
        return nil, err
    }
	if doc == "" {
		log.Debugf("empty html content")
        return nil, fmt.Errorf("empty html content on page: %s", cur)
	}
	
	hashed := sha256.Sum256([]byte(norm))
    document := &model.Document{
        Id: hashed,
        URL: cur.String(),
    }

	c, cancel := context.WithTimeout(ctx, deadlineTime)
	defer cancel()
    links, passages := ws.parseHTMLStream(c, doc, cur, gd)
	if l := len(links); l != 0 {
		ws.lru.Put(hashed, links)
		*pr += math.Log(float64(l) + 1)
	}

	return links, ws.idx.HandleDocumentWords(ctx, document, pr, passages)
}

func (ws *WebScraper) parseHTMLStream(ctx context.Context, htmlContent string, baseURL *url.URL, currentDeep int) (links []*linkToken, pasages []model.Passage) {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var headerType byte
	var garbageTagCounter int
	var isAncore bool
	links = make([]*linkToken, 0, 64)
	visit := make([]*linkToken, 0, 16)

	ws.rlMu.RLock()
	rules := ws.rulesMap[truncatePort(baseURL)]
	ws.rlMu.RUnlock()

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
			if garbageTagCounter > 0 {
				break
			}

			t := tokenizer.Token()
			tagName := t.Data
			switch tagName {
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
							if path := uri.Path; strings.Contains(path, "pdf") || strings.Contains(path, "xml") {
								log.Infof("potential pdf or xml link: %s", uri.String())
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
									log.Infof("potential non-html link: %s", uri.String())
									break
								}
							}
							if rules != nil {
								if !rules.IsAllowed(userAgent, uri.Path)  || !rules.IsAllowed("*", uri.Path){
									break
								}
							}
							isAncore = true
							same := isSameOrigin(uri, baseURL)
							if depth, vis := ws.visited.Load(normalized); vis {
								if depth.(int) > currentDeep {
									visit = append(visit, &linkToken{Link: uri, SameDomain: same})
								}
								break
							}
							links = append(links, &linkToken{Link: uri, SameDomain: same})
						}
						break
					}
				}

			case "script", "style", "iframe", "aside", "nav", "footer":
				garbageTagCounter++

			}

		case html.EndTagToken:
			t := tokenizer.Token()
			tagName := strings.ToLower(t.Data)
			if isAncore && tagName[0] == 'a' {
				isAncore = false
				continue
			}
			if tagName[0] == 'h' && len(tagName) > 1 && (tagName[1] == '1' || tagName[1] == '2') {
				headerType -= tagName[1]
				continue
			}

			if garbageTagCounter > 0 && isGarbage(tagName) {
				garbageTagCounter--
			}

		case html.TextToken:
			if isAncore && len(links) > 0 {
				links[len(links) - 1].Ancore = string(bytes.TrimSpace(tokenizer.Text()))
				continue
			}
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
	if len(visit) != 0 {
		links = append(links, visit...)
	}
	return
}

func isGarbage(tag string) bool {
	garbageTags := []string{"script", "style", "iframe", "aside", "nav", "footer", "div"}
	for _, t := range garbageTags {
		if tag == t {
			return true
		}
	}
	return false
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusTooManyRequests && !ws.checkContext(ctx) {
			<-time.After(deadlineTime)
			return ws.getHTML(ctx, URL, rl, try - 1)
		} else {
			return "", fmt.Errorf("non-200 status code: %d", resp.StatusCode)
		}
	}

	if ws.checkContext(ws.globalCtx) {
		return "", fmt.Errorf("context canceled")
	}

	ctype := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ctype), "text/html") {
		return "", fmt.Errorf("unsupported content type: %s", ctype)
	}

	var builder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	
	for scanner.Scan() {
		select {
		case <-ws.globalCtx.Done():
			return builder.String(), nil
		default:
			builder.WriteString(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	htmlText := builder.String()
	if cset := strings.ToLower(resp.Header.Get("charset")); cset == "" || cset != wantedCharset {
		if match := metaCharsetRe.FindStringSubmatch(htmlText); len(match) > 1 {
			cset = strings.ToLower(strings.TrimSpace(string(match[1])))
		}
		enc, _ := charset.Lookup(cset)
		if enc == nil {
			enc = encoding.Nop
		}
		utf8Bytes, err := io.ReadAll(enc.NewDecoder().Reader(bytes.NewReader([]byte(htmlText))))
		if err != nil {
			return "", err
		}
		htmlText = string(utf8Bytes)
	}
	return htmlText, nil
}