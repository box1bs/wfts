package scraper

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestHtmlGetter(t *testing.T) {
	tests := []struct {
        name     string
        url      string
        filename string
    } {
        {
            name:     "example.com",
            url:      "https://example.com/",
            filename: "../../../../assets/utest.html",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ws, err := NewScraper(&configData{LocalCachePath: "queue.bin", FilterUrl: "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt"}, nil, context.Background())
            if err != nil {
                t.Fatalf("scraper initialization failed: %v", err)
            }
            html, err := ws.getHTML(context.Background(), tt.url, NewRateLimiter(1), 3)
            if err != nil {
                t.Fatalf("getHTML(%q): %v", tt.url, err)
            }
            path, err := filepath.Abs(tt.filename)
            if err != nil {
                t.Fatalf("filepath.Abs(%s): %v", tt.filename, err)
            }
            htmlReader, err := os.OpenFile(path, os.O_RDONLY, 0600)
            if err != nil {
                t.Fatalf("OpenFile(%q): %v", tt.filename, err)
            }
            defer htmlReader.Close()
            
            expectedBytes, err := io.ReadAll(htmlReader)
            if err != nil {
                t.Fatalf("ReadAll(): %v", err)
            }
            html = strings.TrimSuffix(html, "\n")
            expected := string(expectedBytes)
            r1, r2 := []rune(html), []rune(expected)
            if len(r1) != len(r2) {
                t.Errorf("getHTML(%q) = %q...; want %q...\n", 
                    tt.url, html, expected)
                return
            }
            for i := 0; i < len(r1); i++ {
                if r1[i] != r2[i] {
                    t.Errorf("getHTML(%q) = %q...; want %q...\n", 
                        tt.url, html, expected)
                }
            }
        })
    }
}

func TestHaveSitemap(t *testing.T) {
	tests := []struct {
        name     string
        input    string
        expected []string
    } {
        {
            name:  "google sitemap index",
            input: "https://www.google.com/",
            expected: []string{
                "https://www.google.com/gmail/sitemap.xml",
                "https://www.google.com/forms/sitemaps.xml",
                "https://www.google.com/slides/sitemaps.xml",
                "https://www.google.com/sheets/sitemaps.xml",
                "https://www.google.com/drive/sitemap.xml",
                "https://www.google.com/docs/sitemaps.xml",
                "https://www.google.com/get/sitemap.xml",
                "https://www.google.com/travel/flights/sitemap.xml",
                "https://www.google.com/admob/sitemap.xml",
                "https://www.google.com/partners/about/sitemap.xml",
                "https://www.google.com/adwords/sitemap.xml",
                "https://www.google.com/adsense/start/sitemap.xml",
                "https://www.google.com/chromebook/sitemap.xml",
                "https://www.google.com/chrome/sitemap.xml",
                "https://www.google.com/calendar/about/sitemap.xml",
                "https://www.google.com/photos/sitemap.xml",
                "https://www.google.com/nonprofits/sitemap.xml",
                "https://www.google.com/finance/sitemap.xml",
                "https://www.google.com/shopping/sitemap.xml",
                "https://www.google.com/grants/sitemap.xml",
            },
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            parsed, _ := url.Parse(tt.input)
            ws, err := NewScraper(&configData{LocalCachePath: "queue.bin", FilterUrl: "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt"}, nil, context.Background())
            if err != nil {
                t.Fatalf("scraper initialization failed: %v", err)
            }
            links, err := ws.haveSitemap(parsed)
            if err != nil {
                t.Fatalf("haveSitemap(%q): %v", tt.input, err)
            }
            sort.Strings(links)
            sort.Strings(tt.expected)
            
            if !reflect.DeepEqual(links, tt.expected) {
                t.Errorf("haveSitemap(%q) = %v; want %v",
					tt.input, links, tt.expected)
            }
        })
    }
}

func TestNormalizeUrl(t *testing.T) {
	tests := []struct {
        name     string
        input    string
        expected string
    } {
        {
			name: "www prefix", 
			input: "https://www.example.com/", 
			expected: "example.com",
		},
        {
			name: "double slash", 
			input: "https://example.com//", 
			expected: "example.com",
		},
        {
			name: "query params", 
			input: "https://example.com/?id=1", 
			expected: "example.com?id=1",
		},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            uri, _ := url.Parse(tt.input)
            result, err := normalizeUrl(uri)
            if result != tt.expected || err != nil {
                t.Errorf("normalizeUrl(%q) = %q, %v; want %q",
                    tt.input, result, err, tt.expected)
            }
        })
    }
}

func TestResettingUrl(t *testing.T) {
	tests := []struct {
        name     string
        input    string
        expected string
    } {
        {
			name: "general", 
			input: "https://www.example.com/", 
			expected: "https://www.example.com/",
		},
        {
			name: "not empty path", 
			input: "https://hub.docker.com/repositories/korobo4ka", 
			expected: "https://hub.docker.com/",
		},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            uri, _ := url.Parse(tt.input)
            result := resetUrl(uri)
            if result != tt.expected {
                t.Errorf("resetUrl(%q) = %q; want %q",
                    tt.input, result, tt.expected)
            }
        })
    }
}

func TestDomainDefinition(t *testing.T) {
	parsedUrl1, _ := url.Parse("https://google.com/")
	parsedUrl2, _ := url.Parse("https://www.google.com/search?q=thfjngjkyk&sca_esv=492d03d456b59a14&sxsrf=ANbL-n6qW_s1ov2p-JUzb8lBX_hM-2ECbw%3A1768497888610&source=hp&ei=4CJpafXUI5yzi-gP_tXY4Ac&iflsig=AFdpzrgAAAAAaWkw8E8wYLwh1iURDenMKQfHKOYRIK5S&ved=0ahUKEwj1xL2DiI6SAxWc2QIHHf4qFnwQ4dUDCB4&uact=5&oq=thfjngjkyk&gs_lp=Egdnd3Mtd2l6Igp0aGZqbmdqa3lrMgUQABjvBTIIEAAYgAQYogQyCBAAGIAEGKIEMggQABiABBiiBDIFEAAY7wVIkwtQlwJYqgdwAXgAkAECmAHZAaABuQqqAQUwLjkuMbgBA8gBAPgBAZgCCaACngioAgrCAg0QIxiABBgnGIoFGOoCwgIHECMYJxjqAsICChAjGIAEGCcYigXCAgoQLhiABBhDGIoFwgIKEAAYgAQYQxiKBcICBRAAGIAEwgILEC4YgAQY0QMYxwHCAgUQLhiABMICBxAAGIAEGArCAgkQABiABBgKGAvCAgsQABiABBgBGAoYC8ICBxAuGIAEGA3CAgkQABiABBgKGA3CAgYQABgNGB7CAgcQABiABBgNwgILEAAYgAQYkgMYigXCAgoQABiABBjJAxgNmAMR8QWwBqhiWbg58JIHAzEuOKAH6UyyBwMwLji4B40IwgcHMC4zLjMuM8gHLoAIAA&sclient=gws-wiz")
	parsedUrl3, _ := url.Parse("https://support.google.com/")
	parsedUrl4, _ := url.Parse("https://domains.google/")
	tests := []struct{
		name 		string
		in 	 		[2]*url.URL
		expected 	bool
	}{
		{
			name: "query", 
			in: [2]*url.URL{parsedUrl1, parsedUrl2}, 
			expected: true,
		},
		{
			name: "subdomain", 
			in: [2]*url.URL{parsedUrl1, parsedUrl3}, 
			expected: true,
		},
		{
			name: "another url", 
			in: [2]*url.URL{parsedUrl1, parsedUrl4}, 
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if same := isSameOrigin(tt.in[0], tt.in[1]); same != tt.expected {
				t.Errorf("sameOrigin(%s, %s): %t, want %t", tt.in[0].String(), tt.in[1].String(), same, tt.expected)
			}
		})
	}
}