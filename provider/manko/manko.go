package manko

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/flaresolverr"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/javnumber"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*Manko)(nil)
	_ provider.MovieSearcher = (*Manko)(nil)
	_ provider.Fetcher       = (*Manko)(nil)

	hashIDRe            = regexp.MustCompile(`(?i)^[0-9a-f]{24}$`)
	backgroundImageRe   = regexp.MustCompile(`(?i)background-image\s*:\s*url\(\s*['"]?([^'")]+)`)
	variantSuffixRe     = regexp.MustCompile(`(?i)(?:^|[-_\s])(?:uncensored(?:[-_\s]leak(?:ed)?)?|leak(?:ed)?|english[-_\s]subtitle|chinese[-_\s]subtitle|japanese[-_\s]subtitle|subtitle|sub)$`)
	emptyAppShellRe     = regexp.MustCompile(`(?is)<div[^>]*\bid\s*=\s*["']app["'][^>]*>\s*</div>`)
	errAntiBotChallenge = errors.New("Manko returned a Cloudflare challenge")
)

const (
	Name     = "Manko"
	Priority = 1000 - 21
	baseURL  = "https://manko.fun/"
)

// Manko provides metadata from Manko's search and detail pages.
type Manko struct {
	*fetch.Fetcher
	*scraper.Scraper

	flareMu     sync.RWMutex
	flareClient *flaresolverr.Client
}

func New() *Manko {
	return &Manko{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.English,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "en-US,en;q=0.9",
			}),
		),
	}
}

func (m *Manko) SetFlareSolverr(endpoint string) error {
	if err := m.Scraper.SetFlareSolverr(endpoint); err != nil {
		return err
	}

	var client *flaresolverr.Client
	if strings.TrimSpace(endpoint) != "" {
		var err error
		client, err = flaresolverr.NewClient(endpoint, time.Minute)
		if err != nil {
			return err
		}
	}

	m.flareMu.Lock()
	m.flareClient = client
	m.flareMu.Unlock()
	return nil
}

func (m *Manko) NormalizeMovieID(id string) string {
	raw := cleanText(id)
	if normalized := javnumber.Normalize(raw); normalized != "" {
		return normalized
	}
	return normalizeHashID(raw)
}

func (m *Manko) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !m.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	segments := splitPath(u.Path)
	for i := 0; i+1 < len(segments); i++ {
		if !strings.EqualFold(segments[i], "movie-info") {
			continue
		}
		if id := normalizeHashID(segments[i+1]); id != "" {
			return id, nil
		}
	}
	return "", provider.ErrInvalidURL
}

func (m *Manko) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = m.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	if hashIDRe.MatchString(id) {
		return m.getMovieInfoByURL(m.detailURL(id), id)
	}

	results, err := m.SearchMovie(id)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return m.getMovieInfoByURL(results[0].Homepage, results[0].ID)
}

func (m *Manko) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := m.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return m.getMovieInfoByURL(rawURL, id)
}

func (m *Manko) NormalizeMovieKeyword(keyword string) string {
	return javnumber.Normalize(number.Trim(cleanText(keyword)))
}

func (m *Manko) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = m.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	body, err := m.fetchPage(m.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	return m.parseSearchResults(body, keyword)
}

func (m *Manko) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := m.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return m.parseMovieInfo(body, id, rawURL)
}

func (m *Manko) searchURL(keyword string) string {
	return m.URL().ResolveReference(&url.URL{
		Path: "/searchresult",
		RawQuery: url.Values{
			"by":      {"Title"},
			"keyword": {keyword},
		}.Encode(),
	}).String()
}

func (m *Manko) detailURL(id string) string {
	return m.URL().ResolveReference(&url.URL{
		Path: "/movie-info/" + id,
	}).String()
}

func (m *Manko) fetchPage(rawURL string) ([]byte, error) {
	c := m.ClonedCollector()

	var (
		body        []byte
		responseErr error
	)
	c.OnResponse(func(r *colly.Response) {
		body = append([]byte(nil), r.Body...)
	})
	c.OnError(func(r *colly.Response, err error) {
		if r == nil {
			responseErr = fmt.Errorf("%s: request failed: %w", Name, err)
			return
		}
		if len(r.Body) > 0 {
			body = append([]byte(nil), r.Body...)
		}
		responseErr = fmt.Errorf("%s: request failed with HTTP %d: %w", Name, r.StatusCode, err)
	})

	visitErr := c.Visit(rawURL)
	if len(body) > 0 {
		if err := detectPageError(body); err != nil {
			return nil, err
		}
	}
	if visitErr != nil {
		if responseErr != nil {
			return nil, responseErr
		}
		return nil, visitErr
	}
	if responseErr != nil {
		return nil, responseErr
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%s: empty response", Name)
	}
	if isSPAAppShell(body) {
		return m.fetchRenderedPage(rawURL)
	}
	return body, nil
}

func (m *Manko) fetchRenderedPage(rawURL string) ([]byte, error) {
	m.flareMu.RLock()
	client := m.flareClient
	m.flareMu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("%s: SPA page requires FlareSolverr", Name)
	}

	headers := make(http.Header)
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Referer", m.URL().String())
	body, err := client.Get(context.Background(), rawURL, headers)
	if err != nil {
		return nil, fmt.Errorf("%s: render SPA page with FlareSolverr: %w", Name, err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%s: FlareSolverr returned an empty response", Name)
	}
	if err := detectPageError(body); err != nil {
		return nil, err
	}
	if isSPAAppShell(body) {
		return nil, fmt.Errorf("%s: FlareSolverr did not render SPA content", Name)
	}
	return body, nil
}

func (m *Manko) parseSearchResults(body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	doc.Find(".border.border-gray-dark").Each(func(_ int, card *goquery.Selection) {
		link := card.Find("a[href*='/movie-info/'][title]").First()
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		homepage := m.absoluteURL(href)
		if _, parseErr := m.ParseMovieIDFromURL(homepage); parseErr != nil {
			return
		}

		id := normalizeSearchNumber(firstAttr(link, "title"))
		if id == "" || !strings.EqualFold(id, keyword) {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		cover := ""
		if style, ok := card.Find(`div[style*="background-image"]`).First().Attr("style"); ok {
			if match := backgroundImageRe.FindStringSubmatch(style); len(match) == 2 {
				cover = m.absoluteURL(match[1])
			}
		}

		result := &model.MovieSearchResult{
			ID:          id,
			Number:      id,
			Title:       id,
			Provider:    Name,
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			Score:       parser.ParseScore(firstText(card.Find(`button[aria-label*="rating"]`).First())),
			ReleaseDate: parser.ParseDate(firstText(card.Find("span.text-gray-300").First())),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return results, nil
}

func (m *Manko) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		Provider:      Name,
		Homepage:      homepage,
		Title:         firstText(doc.Find("h1").First()),
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	doc.Find("div.space-y-2.text-sm > div").Each(func(_ int, row *goquery.Selection) {
		label := strings.Trim(firstText(row.Find("span.text-gray-400.font-medium").First()), ":：")
		value := firstText(row.Find("span.text-white").First())
		if value == "" {
			value = firstText(row.Find("span").Eq(1))
		}
		switch strings.ToLower(label) {
		case "title":
			if n := normalizeSearchNumber(value); n != "" {
				info.Number = n
			}
		case "rating":
			info.Score = parser.ParseScore(value)
		case "video duration":
			info.Runtime = parser.ParseRuntime(value)
		case "maker":
			info.Maker = value
		case "release date":
			info.ReleaseDate = parser.ParseDate(value)
		case "actor":
			row.Find("a[href*='/actor/']").Each(func(_ int, actor *goquery.Selection) {
				info.Actors = appendUnique(info.Actors, trimFact(actor.Text()))
			})
			if len(info.Actors) == 0 {
				info.Actors = appendUnique(info.Actors, value)
			}
		}
	})

	doc.Find("a[href*='/filter/genre/']").Each(func(_ int, genre *goquery.Selection) {
		info.Genres = appendUnique(info.Genres, genre.Text())
	})
	doc.Find(`img[alt^="Backdrop"]`).Each(func(_ int, image *goquery.Selection) {
		info.PreviewImages = appendUnique(info.PreviewImages,
			m.absoluteURL(firstAttr(image, "src", "data-src")))
	})

	doc.Find("iframe[src]").EachWithBreak(func(_ int, iframe *goquery.Selection) bool {
		info.CoverURL = coverURLFromEmbed(firstAttr(iframe, "src"))
		if info.CoverURL != "" {
			info.CoverURL = m.absoluteURL(info.CoverURL)
			info.ThumbURL = info.CoverURL
			return false
		}
		return true
	})

	if info.Number == "" {
		info.Number = javnumber.Find(info.Title)
	}
	if info.Number == "" && !hashIDRe.MatchString(id) {
		info.Number = id
	}
	info.ID = info.Number
	info.Title = javnumber.CleanTitle(info.Title, info.Number)

	if info.ID == "" || info.Title == "" || info.Number == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (m *Manko) isSiteHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, candidate := range []string{m.URL().Hostname(), "manko.fun"} {
		candidate = strings.TrimPrefix(strings.ToLower(candidate), "www.")
		if host == candidate || host == "www."+candidate {
			return true
		}
	}
	return false
}

func (m *Manko) absoluteURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return ""
	}
	if strings.HasPrefix(rawURL, "//") {
		rawURL = "https:" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return m.URL().ResolveReference(u).String()
}

func normalizeHashID(raw string) string {
	raw = strings.Trim(cleanText(raw), "/ ")
	if !hashIDRe.MatchString(raw) {
		return ""
	}
	return strings.ToLower(raw)
}

func normalizeSearchNumber(raw string) string {
	raw = cleanText(raw)
	if raw == "" || variantSuffixRe.MatchString(raw) {
		return ""
	}
	return javnumber.Normalize(raw)
}

func coverURLFromEmbed(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	poster := strings.TrimSpace(u.Query().Get("poster"))
	posterURL, err := url.Parse(poster)
	if err != nil || (posterURL.Scheme != "http" && posterURL.Scheme != "https") ||
		posterURL.Hostname() == "" {
		return ""
	}
	return posterURL.String()
}

func trimFact(value string) string {
	return strings.Trim(cleanText(value), ",，、;；")
}

func splitPath(value string) []string {
	raw := strings.Split(strings.Trim(value, "/"), "/")
	segments := make([]string, 0, len(raw))
	for _, segment := range raw {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "<title>just a moment...</title>") ||
		strings.Contains(lower, "<title>attention required! | cloudflare</title>") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "__cf_chl_") ||
		strings.Contains(lower, `id="challenge-form"`) ||
		strings.Contains(lower, `id='challenge-form'`) {
		return errAntiBotChallenge
	}
	return nil
}

func isSPAAppShell(body []byte) bool {
	content := strings.ToLower(string(body))
	return strings.Contains(content, "/dist/build.js") &&
		emptyAppShellRe.MatchString(content)
}

func firstText(selection *goquery.Selection) string {
	if selection.Length() == 0 {
		return ""
	}
	return cleanText(selection.First().Text())
}

func firstAttr(selection *goquery.Selection, names ...string) string {
	for _, name := range names {
		if value, ok := selection.Attr(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cleanText(value string) string {
	return norm.NFC.String(javnumber.CleanText(value))
}

func appendUnique(values []string, value string) []string {
	value = cleanText(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func init() {
	provider.Register(Name, New)
}
