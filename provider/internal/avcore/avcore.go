package avcore

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

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/flaresolverr"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/javnumber"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	detailIDRe          = regexp.MustCompile(`(?i)^[a-z][a-z0-9]{4,}$`)
	datePattern         = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	runtimePattern      = regexp.MustCompile(`(\d+)\s*分`)
	errAntiBotChallenge = errors.New("AV provider returned an anti-bot challenge")
)

var (
	_ provider.MovieProvider = (*Provider)(nil)
	_ provider.MovieSearcher = (*Provider)(nil)
	_ provider.Fetcher       = (*Provider)(nil)
)

// Provider implements the common frontend used by AVMOO and AVSOX.
type Provider struct {
	*fetch.Fetcher
	*scraper.Scraper

	flareMu     sync.RWMutex
	flareClient *flaresolverr.Client
}

func New(name, base string, priority float64) *Provider {
	return &Provider{
		Fetcher: fetch.Default(&fetch.Config{Referer: base}),
		Scraper: scraper.NewDefaultScraper(name, base, priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         base,
				"Accept-Language": "ja-JP,ja;q=0.9",
			})),
	}
}

func (p *Provider) SetFlareSolverr(endpoint string) error {
	if err := p.Scraper.SetFlareSolverr(endpoint); err != nil {
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
	p.flareMu.Lock()
	p.flareClient = client
	p.flareMu.Unlock()
	return nil
}

func (p *Provider) NormalizeMovieID(id string) string {
	id = javnumber.CleanText(id)
	id = strings.TrimSuffix(id, ".html")
	if detailIDRe.MatchString(id) {
		return strings.ToLower(id)
	}
	if keyword := javnumber.Normalize(id); keyword != "" {
		return keyword
	}
	return ""
}

func (p *Provider) NormalizeMovieKeyword(keyword string) string {
	return javnumber.Normalize(keyword)
}

func (p *Provider) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !p.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	segments := splitPath(u.Path)
	for i, segment := range segments {
		if i+1 >= len(segments) {
			continue
		}
		switch strings.ToLower(segment) {
		case "search":
			if id := p.NormalizeMovieKeyword(segments[i+1]); id != "" {
				return id, nil
			}
		case "movies":
			if id := p.NormalizeMovieID(segments[i+1]); id != "" && !strings.Contains(id, "-") {
				return id, nil
			}
		}
	}
	return "", provider.ErrInvalidURL
}

func (p *Provider) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = p.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	if keyword := p.NormalizeMovieKeyword(id); keyword != "" {
		results, err := p.SearchMovie(keyword)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, provider.ErrInfoNotFound
		}
		return p.getMovieInfoByURL(results[0].Homepage, results[0].ID)
	}
	return p.getMovieInfoByURL(p.detailURL(id), id)
}

func (p *Provider) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := p.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(rawURL)
	if containsPathSegment(u.Path, "movies") {
		return p.getMovieInfoByURL(p.absoluteURL(rawURL), id)
	}
	return p.GetMovieInfoByID(id)
}

func (p *Provider) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = p.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	body, err := p.fetchPage(p.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	results, err := p.parseSearchResults(body, keyword)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return results, nil
}

func (p *Provider) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := p.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return p.parseMovieInfo(body, id, rawURL)
}

func (p *Provider) fetchPage(rawURL string) ([]byte, error) {
	c := p.ClonedCollector()

	var (
		body        []byte
		responseErr error
	)
	c.OnResponse(func(r *colly.Response) {
		body = append([]byte(nil), r.Body...)
	})
	c.OnError(func(r *colly.Response, err error) {
		if r == nil {
			responseErr = fmt.Errorf("%s: request failed: %w", p.Name(), err)
			return
		}
		if len(r.Body) > 0 {
			body = append([]byte(nil), r.Body...)
		}
		responseErr = fmt.Errorf("%s: request failed with HTTP %d: %w", p.Name(), r.StatusCode, err)
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
		return nil, fmt.Errorf("%s: empty response", p.Name())
	}
	if isSPAAppShell(body) {
		return p.fetchRenderedPage(rawURL)
	}
	return body, nil
}

func (p *Provider) fetchRenderedPage(rawURL string) ([]byte, error) {
	p.flareMu.RLock()
	client := p.flareClient
	p.flareMu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("%s: SPA page requires FlareSolverr", p.Name())
	}

	headers := make(http.Header)
	headers.Set("Accept-Language", "ja-JP,ja;q=0.9")
	headers.Set("Referer", p.URL().String())
	body, err := client.Get(context.Background(), rawURL, headers)
	if err != nil {
		return nil, fmt.Errorf("%s: render SPA page with FlareSolverr: %w", p.Name(), err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%s: FlareSolverr returned an empty response", p.Name())
	}
	if err := detectPageError(body); err != nil {
		return nil, err
	}
	if isSPAAppShell(body) {
		return nil, fmt.Errorf("%s: FlareSolverr did not render SPA content", p.Name())
	}
	return body, nil
}

func (p *Provider) parseSearchResults(body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", p.Name(), err)
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	doc.Find("a.movie-card").Each(func(_ int, item *goquery.Selection) {
		href, ok := item.Attr("href")
		if !ok {
			return
		}
		homepage := p.absoluteURL(href)
		id, parseErr := p.ParseMovieIDFromURL(homepage)
		if parseErr != nil {
			return
		}

		title := firstText(item.Find(".movie-name").First())
		metadata := make([]string, 0)
		item.Find(".movie-meta span").Each(func(_ int, meta *goquery.Selection) {
			metadata = append(metadata, cleanText(meta.Text()))
		})
		number := javnumber.Find(title)
		if number == "" {
			number = javnumber.Find(strings.Join(metadata, " "))
		}
		if number != keyword {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		releaseDate := strings.Join(metadata, " ")
		if match := datePattern.FindString(releaseDate); match != "" {
			releaseDate = match
		}
		cover := p.absoluteURL(firstAttr(item.Find("img").First(),
			"data-src", "data-original", "src"))
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      number,
			Title:       javnumber.CleanTitle(title, number),
			Provider:    p.Name(),
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(releaseDate),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) == 0 {
		if doc.Find("a.movie-card").Length() == 0 ||
			strings.Contains(strings.ToLower(doc.Text()), "結果がありません") ||
			strings.Contains(strings.ToLower(doc.Text()), "no results") {
			return nil, provider.ErrInfoNotFound
		}
		return nil, provider.ErrInfoNotFound
	}
	return results, nil
}

func (p *Provider) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", p.Name(), err)
	}

	title := firstText(doc.Find("h1").First())
	if title == "" {
		title = firstAttr(doc.Find(`meta[property="og:title"]`).First(), "content")
	}
	info := &model.MovieInfo{
		ID:            id,
		Provider:      p.Name(),
		Homepage:      homepage,
		Title:         title,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	cover := doc.Find(".q-img.poster-image img").First()
	if cover.Length() == 0 {
		cover = doc.Find(`meta[property="og:image"]`).First()
	}
	info.CoverURL = p.absoluteURL(firstAttr(cover,
		"data-src", "data-original", "src", "content"))
	info.ThumbURL = info.CoverURL
	if info.CoverURL == "" {
		info.CoverURL = p.absoluteURL(firstAttr(doc.Find(
			`.poster-image img, .cover img, .movie-cover img`).First(),
			"data-src", "data-original", "src"))
		info.ThumbURL = info.CoverURL
	}

	doc.Find(".detail-item").Each(func(_ int, item *goquery.Selection) {
		label := strings.Trim(cleanText(item.Find(".detail-label").First().Text()), ":：")
		value := item.Find(".detail-value").First()
		valueText := cleanText(value.Text())
		switch label {
		case "品番", "番号", "ID":
			if number := javnumber.Find(valueText); number != "" {
				info.Number = number
			}
		case "発売日", "配信開始日", "発売開始日":
			if match := datePattern.FindString(valueText); match != "" {
				info.ReleaseDate = parser.ParseDate(match)
			}
		case "収録時間", "再生時間":
			if match := runtimePattern.FindStringSubmatch(valueText); len(match) == 2 {
				info.Runtime = parser.ParseInt(match[1])
			}
		case "監督":
			info.Director = cleanField(doc, item, value)
		case "メーカー", "メーカー名":
			info.Maker = cleanField(doc, item, value)
		case "レーベル":
			info.Label = cleanField(doc, item, value)
		case "シリーズ":
			info.Series = cleanField(doc, item, value)
		case "ジャンル", "タグ":
			item.Find(".detail-link, a").Each(func(_ int, link *goquery.Selection) {
				info.Genres = appendUnique(info.Genres, link.Text())
			})
		}
	})

	if info.Number == "" {
		info.Number = javnumber.Find(title)
	}
	if info.Number == "" {
		info.Number = p.NormalizeMovieKeyword(id)
	}
	info.Title = javnumber.CleanTitle(info.Title, info.Number)

	doc.Find(".detail-item").Each(func(_ int, item *goquery.Selection) {
		if !strings.Contains(cleanText(item.Find(".detail-label").First().Text()), "ジャンル") {
			return
		}
		item.Find(".detail-link, a").Each(func(_ int, link *goquery.Selection) {
			info.Genres = appendUnique(info.Genres, link.Text())
		})
	})
	doc.Find(".actress-name").Each(func(_ int, actor *goquery.Selection) {
		info.Actors = appendUnique(info.Actors, actor.Text())
	})

	for _, selector := range []string{
		".sample-grid img",
		".sample-images img",
		".sample-image img",
	} {
		doc.Find(selector).Each(func(_ int, image *goquery.Selection) {
			info.PreviewImages = appendUnique(info.PreviewImages,
				p.absoluteURL(firstAttr(image, "data-src", "data-original", "src")))
		})
	}

	if description := firstAttr(doc.Find(
		`meta[name="description"], meta[property="og:description"]`).First(), "content"); description != "" {
		info.Summary = cleanText(description)
	}

	if info.Title == "" || info.Number == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", p.Name())
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (p *Provider) searchURL(keyword string) string {
	return p.URL().ResolveReference(&url.URL{
		Path: "/ja/search/" + keyword,
	}).String()
}

func (p *Provider) detailURL(id string) string {
	return p.URL().ResolveReference(&url.URL{
		Path: "/ja/movies/" + id,
	}).String()
}

func (p *Provider) absoluteURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return p.URL().ResolveReference(u).String()
}

func (p *Provider) isSiteHost(host string) bool {
	baseHost := strings.ToLower(p.URL().Hostname())
	host = strings.ToLower(host)
	return host == baseHost ||
		host == "www."+strings.TrimPrefix(baseHost, "www.")
}

func cleanField(_ *goquery.Document, item, value *goquery.Selection) string {
	if value.Length() > 0 {
		if link := value.Find("a").First(); link.Length() > 0 {
			return cleanText(link.Text())
		}
		return cleanText(value.Text())
	}
	field := item.Clone()
	field.Find(".detail-label").Remove()
	return strings.Trim(cleanText(field.Text()), ":：- ")
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

func containsPathSegment(value, want string) bool {
	for _, segment := range splitPath(value) {
		if strings.EqualFold(segment, want) {
			return true
		}
	}
	return false
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "<title>just a moment...</title>") ||
		strings.Contains(lower, "<title>attention required! | cloudflare</title>") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "__cf_chl_") ||
		strings.Contains(lower, "id=\"challenge-form\"") {
		return errAntiBotChallenge
	}
	return nil
}

func isSPAAppShell(body []byte) bool {
	content := strings.ToLower(string(body))
	return strings.Contains(content, `id="jav-site-index"`) &&
		!strings.Contains(content, `class="movie-card`) &&
		!strings.Contains(content, `class="movie-detail`)
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
	return javnumber.CleanText(value)
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
