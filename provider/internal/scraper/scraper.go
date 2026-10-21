package scraper

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"go.uber.org/atomic"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/flaresolverr"
)

var (
	_ provider.Provider             = (*Scraper)(nil)
	_ provider.ProxySetter          = (*Scraper)(nil)
	_ provider.RequestTimeoutSetter = (*Scraper)(nil)
	_ provider.FlareSolverrSetter   = (*Scraper)(nil)
)

// Scraper implements the basic Provider interface.
type Scraper struct {
	name      string
	baseURL   *url.URL
	priority  *atomic.Float64
	language  language.Tag
	c         *colly.Collector
	transport *flaresolverr.Transport
}

// NewScraper returns a *Scraper that implements provider.Provider.
func NewScraper(name, base string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	baseURL, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	transport := flaresolverr.NewTransport(http.DefaultTransport)
	s := &Scraper{
		name:      name,
		baseURL:   baseURL,
		priority:  atomic.NewFloat64(priority),
		language:  lang,
		c:         colly.NewCollector(),
		transport: transport,
	}
	s.c.WithTransport(transport)
	for _, opt := range opts {
		// Apply options.
		if err := opt(s); err != nil {
			panic(err)
		}
	}
	return s
}

// NewDefaultScraper returns a *Scraper with default options enabled.
func NewDefaultScraper(name, baseURL string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	return NewScraper(name, baseURL, priority, lang, append([]Option{
		WithAllowURLRevisit(),
		WithIgnoreRobotsTxt(),
		WithRandomUserAgent(),
	}, opts...)...)
}

func (s *Scraper) Name() string { return s.name }

func (s *Scraper) URL() *url.URL { return s.baseURL }

func (s *Scraper) Priority() float64 { return s.priority.Load() }

func (s *Scraper) SetPriority(v float64) { s.priority.Store(v) }

func (s *Scraper) Language() language.Tag { return s.language }

func (s *Scraper) NormalizeMovieID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseMovieIDFromURL(string) (string, error) { panic("unimplemented") }

func (s *Scraper) NormalizeActorID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseActorIDFromURL(string) (string, error) { panic("unimplemented") }

// ClonedCollector returns cloned internal collector.
func (s *Scraper) ClonedCollector() *colly.Collector { return s.c.Clone() }

// SetProxy sets http or socks5 proxy for HTTP requests.
func (s *Scraper) SetProxy(proxyURL string) error {
	if !strings.Contains(proxyURL, "://") {
		proxyURL = "http://" + proxyURL
	}
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}

	base := s.transport.Base()
	transport, ok := base.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.Proxy = http.ProxyURL(proxy)
	transport.DisableKeepAlives = true
	s.transport.SetBase(transport)
	return nil
}

// SetRequestTimeout sets timeout for HTTP requests.
func (s *Scraper) SetRequestTimeout(timeout time.Duration) {
	s.c.SetRequestTimeout(timeout)
	s.transport.SetTimeout(timeout)
}

// SetFlareSolverr enables Cloudflare challenge solving for this scraper.
func (s *Scraper) SetFlareSolverr(endpoint string) error {
	return s.transport.SetEndpoint(endpoint)
}

func (s *Scraper) setTransport(transport http.RoundTripper) {
	s.transport.SetBase(transport)
}
