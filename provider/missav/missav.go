package missav

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/domainpool"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/javnumber"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*MissAV)(nil)
	_ provider.MovieSearcher = (*MissAV)(nil)
	_ provider.Fetcher       = (*MissAV)(nil)
	_ provider.DomainSetter  = (*MissAV)(nil)
	_ provider.URLMatcher    = (*MissAV)(nil)

	detailSlugRe        = regexp.MustCompile(`(?i)^[a-z][a-z0-9]{0,31}[-_\s]?\d{2,8}$`)
	variantSlugRe       = regexp.MustCompile(`(?i)(?:uncensored(?:-leak)?|leak|english-subtitle|chinese-subtitle|japanese-subtitle|subtitle)$`)
	errAntiBotChallenge = errors.New("MissAV returned an anti-bot challenge")
)

const (
	Name     = "MissAV"
	Priority = 1000 - 18
	baseURL  = "https://missav.ws/"
)

// MissAV provides metadata from MissAV's Japanese search and detail pages.
type MissAV struct {
	*fetch.Fetcher
	*scraper.Scraper
	domains *domainpool.Pool
}

func New() *MissAV {
	return &MissAV{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			})),
		domains: domainpool.New(),
	}
}

func (m *MissAV) SetBackupDomains(domains []string) error {
	return m.domains.SetBackups(domains)
}

func (m *MissAV) Domains() []string {
	return m.domains.Domains(m.URL())
}

func (m *MissAV) MatchesURL(u *url.URL) bool {
	return u != nil && (m.domains.Matches(m.URL(), u) || m.isSiteHost(u.Hostname()))
}

func (m *MissAV) NormalizeMovieID(id string) string {
	return normalizeDetailID(id)
}

func (m *MissAV) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !m.MatchesURL(u) {
		return "", provider.ErrInvalidURL
	}

	segments := splitPath(u.Path)
	for _, segment := range segments {
		if variantSlugRe.MatchString(segment) {
			return "", provider.ErrInvalidURL
		}
	}
	for i, segment := range segments {
		if (strings.EqualFold(segment, "search") || strings.EqualFold(segment, "ja")) &&
			i+1 < len(segments) {
			if id := normalizeDetailID(segments[i+1]); id != "" {
				return id, nil
			}
		}
	}
	for i := len(segments) - 1; i >= 0; i-- {
		if id := normalizeDetailID(segments[i]); id != "" {
			return id, nil
		}
	}
	return "", provider.ErrInvalidURL
}

func (m *MissAV) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = m.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}

	var lastErr error
	for _, base := range m.domains.Candidates(m.URL()) {
		results, err := m.searchMovie(base, id)
		if err != nil {
			if stopDomainFallback(err) {
				return nil, err
			}
			lastErr = err
			continue
		}
		if len(results) == 0 {
			return nil, provider.ErrInfoNotFound
		}

		info, err := m.getMovieInfoByURL(base, results[0].Homepage, results[0].ID)
		if err == nil {
			return info, nil
		}
		if stopDomainFallback(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (m *MissAV) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := m.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return m.GetMovieInfoByID(id)
}

func (m *MissAV) NormalizeMovieKeyword(keyword string) string {
	return javnumber.Normalize(number.Trim(cleanText(keyword)))
}

func (m *MissAV) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = m.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}

	var lastErr error
	for _, base := range m.domains.Candidates(m.URL()) {
		results, err := m.searchMovie(base, keyword)
		if err == nil {
			return results, nil
		}
		if stopDomainFallback(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (m *MissAV) searchMovie(base *url.URL, keyword string) ([]*model.MovieSearchResult, error) {
	body, err := m.fetchPage(m.searchURL(base, keyword))
	if err != nil {
		return nil, err
	}
	return m.parseSearchResults(base, body, keyword)
}

func (m *MissAV) getMovieInfoByURL(base *url.URL, rawURL, id string) (*model.MovieInfo, error) {
	body, err := m.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return m.parseMovieInfo(base, body, id, rawURL)
}

func (m *MissAV) searchURL(base *url.URL, keyword string) string {
	return base.ResolveReference(&url.URL{
		Path: "/ja/search/" + keyword,
	}).String()
}

func (m *MissAV) fetchPage(rawURL string) ([]byte, error) {
	c := m.ClonedCollector()

	var (
		body        []byte
		responseErr error
	)
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		referer := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}).String()
		c.OnRequest(func(r *colly.Request) {
			r.Headers.Set("Referer", referer)
		})
	}
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
	return body, nil
}

func (m *MissAV) parseSearchResults(base *url.URL, body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	items := doc.Find(".thumbnail.group")
	items.Each(func(_ int, item *goquery.Selection) {
		link := item.Find(".my-2 a").First()
		if link.Length() == 0 {
			link = item.Find("a[href]").First()
		}
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		homepage := m.absoluteURL(base, href)
		id, parseErr := m.ParseMovieIDFromURL(homepage)
		if parseErr != nil || !strings.EqualFold(id, keyword) {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		title := firstAttr(link, "title")
		if title == "" {
			title = firstText(link)
		}
		if title == "" {
			title = id
		}
		cover := m.absoluteURL(base, firstAttr(item.Find(".thumbnail img").First(),
			"data-src", "data-original", "src"))
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      id,
			Title:       javnumber.CleanTitle(title, id),
			Provider:    m.Name(),
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(item.Find("time").First().Text()),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) == 0 {
		if items.Length() > 0 || hasNoSearchResults(doc) {
			return []*model.MovieSearchResult{}, nil
		}
		return nil, fmt.Errorf("%s: unrecognized search response", Name)
	}
	return results, nil
}

func (m *MissAV) parseMovieInfo(base *url.URL, body []byte, id,
	homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Provider:      m.Name(),
		Homepage:      homepage,
		Title:         firstText(doc.Find("h1").First()),
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}
	if info.Title == "" {
		info.Title = firstAttr(doc.Find(`meta[property="og:title"]`).First(), "content")
	}

	info.CoverURL = m.absoluteURL(base,
		firstAttr(doc.Find(`meta[property="og:image"]`).First(), "content"))
	video := doc.Find("video.player").First()
	if info.CoverURL == "" {
		info.CoverURL = m.absoluteURL(base, firstAttr(video, "data-poster", "poster"))
	}
	info.ThumbURL = info.CoverURL

	doc.Find("div.text-secondary").Each(func(_ int, row *goquery.Selection) {
		labelNode := row.Find("span").First()
		if labelNode.Length() == 0 {
			return
		}
		labelText := cleanText(labelNode.Text())
		label := strings.Trim(labelText, ":：")
		value := cleanText(strings.TrimPrefix(cleanText(row.Text()), labelText))
		value = strings.Trim(value, ":：- ")

		switch label {
		case "品番", "番号", "ID":
			if n := javnumber.Normalize(value); n != "" {
				info.Number = n
			}
		case "配信開始日", "発売日", "発売開始日":
			if date := parser.ParseDate(value); !time.Time(date).IsZero() {
				info.ReleaseDate = date
			}
		case "女優", "出演者":
			row.Find("a").Each(func(_ int, actor *goquery.Selection) {
				info.Actors = appendUnique(info.Actors, actor.Text())
			})
			if len(info.Actors) == 0 {
				info.Actors = appendUnique(info.Actors, value)
			}
		case "ジャンル", "タグ":
			row.Find("a").Each(func(_ int, genre *goquery.Selection) {
				info.Genres = appendUnique(info.Genres, genre.Text())
			})
		case "シリーズ":
			info.Series = value
		case "メーカー", "メーカー名":
			info.Maker = value
		case "監督":
			info.Director = value
		case "レーベル":
			info.Label = value
		}
	})

	if summary := firstText(doc.Find("div.text-secondary.break-all").First()); summary != "" {
		info.Summary = cleanText(summary)
	} else if summary := firstAttr(doc.Find(`meta[property="og:description"]`).First(), "content"); summary != "" {
		info.Summary = cleanText(summary)
	} else if summary := firstAttr(doc.Find(`meta[name="description"]`).First(), "content"); summary != "" {
		info.Summary = cleanText(summary)
	}
	if duration := parser.ParseInt(firstAttr(
		doc.Find(`meta[property="og:video:duration"]`).First(), "content")); duration > 0 {
		info.Runtime = duration / 60
	}
	info.PreviewVideoURL = m.absoluteURL(base, firstAttr(doc.Find(
		"video.player source, video.player").First(), "data-src", "src"))
	if !isHTTPURL(info.PreviewVideoURL) {
		info.PreviewVideoURL = previewURLFromCover(info.CoverURL)
	}

	if info.Number == "" {
		info.Number = javnumber.Find(info.Title)
	}
	if info.Number == "" {
		info.Number = id
	}
	info.Title = javnumber.CleanTitle(info.Title, info.Number)

	if info.Title == "" || info.Number == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (m *MissAV) isSiteHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	hosts := []string{m.URL().Hostname(), "missav.ws", "missav.ai", "missav123.com"}
	for _, raw := range hosts {
		baseHost := strings.TrimPrefix(strings.ToLower(raw), "www.")
		if host == baseHost || host == "www."+baseHost {
			return true
		}
	}
	return false
}

func (m *MissAV) absoluteURL(base *url.URL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

func stopDomainFallback(err error) bool {
	return errors.Is(err, provider.ErrInfoNotFound) ||
		errors.Is(err, provider.ErrInvalidID) ||
		errors.Is(err, provider.ErrInvalidKeyword)
}

func hasNoSearchResults(doc *goquery.Document) bool {
	text := strings.ToLower(cleanText(doc.Text()))
	for _, marker := range []string{
		"no results",
		"not found",
		"見つかりませんでした",
		"該当する作品はありません",
		"検索結果がありません",
		"0件",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isHTTPURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func previewURLFromCover(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	segments := splitPath(u.Path)
	if len(segments) < 2 {
		return ""
	}
	preview := *u
	preview.Path = path.Join("/", segments[len(segments)-2], "preview.mp4")
	preview.RawPath = ""
	preview.RawQuery = ""
	preview.Fragment = ""
	return preview.String()
}

func normalizeDetailID(raw string) string {
	raw = cleanText(raw)
	raw = strings.TrimSuffix(raw, path.Ext(raw))
	raw = strings.Trim(raw, "/ ")
	if raw == "" || !detailSlugRe.MatchString(raw) {
		return ""
	}
	return javnumber.Normalize(raw)
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
		strings.Contains(lower, "id=\"challenge-form\"") {
		return errAntiBotChallenge
	}
	return nil
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

func init() {
	provider.Register(Name, New)
}
