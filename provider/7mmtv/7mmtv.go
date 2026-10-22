package mmtv

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
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*MMTV)(nil)
	_ provider.MovieSearcher = (*MMTV)(nil)
	_ provider.Fetcher       = (*MMTV)(nil)

	labelNumberRe     = regexp.MustCompile(`(?i)^([a-z][a-z0-9]*?)\s*[-_\s]?\s*(\d+)$`)
	runtimeRe         = regexp.MustCompile(`(\d+)\s*分`)
	previewScriptRe   = regexp.MustCompile(`fullvideo_showallcg_arr\[\d+\]\s*=\s*['"]([^'"]+)['"]`)
	errAntiBotRequest = errors.New("7MMTV returned a Cloudflare challenge")
)

const (
	Name     = "7MMTV"
	Priority = 1000 - 15
	baseURL  = "https://7mmtv.sx/"
)

type MMTV struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *MMTV {
	return &MMTV{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(
			Name,
			baseURL,
			Priority,
			language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			}),
		),
	}
}

func (m *MMTV) NormalizeMovieID(id string) string {
	id = cleanText(id)
	if strings.HasSuffix(strings.ToLower(id), ".html") {
		id = strings.TrimSuffix(id, path.Ext(id))
	}
	match := labelNumberRe.FindStringSubmatch(id)
	if len(match) != 3 {
		return ""
	}
	return strings.ToUpper(match[1]) + "-" + match[2]
}

func (m *MMTV) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !m.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	if id := m.NormalizeMovieID(u.Query().Get("search_keyword")); id != "" {
		return id, nil
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) == 0 {
		return "", provider.ErrInvalidURL
	}
	last := segments[len(segments)-1]
	if strings.EqualFold(path.Ext(last), ".html") {
		if id := m.NormalizeMovieID(strings.TrimSuffix(last, path.Ext(last))); id != "" {
			return id, nil
		}
	}
	for _, segment := range segments {
		if id := m.NormalizeMovieID(segment); id != "" {
			return id, nil
		}
	}
	return "", provider.ErrInvalidURL
}

func (m *MMTV) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = m.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	results, err := m.SearchMovie(id)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return m.GetMovieInfoByURL(results[0].Homepage)
}

func (m *MMTV) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := m.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	body, err := m.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return m.parseMovieInfo(body, id, rawURL)
}

func (m *MMTV) NormalizeMovieKeyword(keyword string) string {
	return m.NormalizeMovieID(number.Trim(cleanText(keyword)))
}

func (m *MMTV) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
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

func (m *MMTV) searchURL(keyword string) string {
	return m.URL().ResolveReference(&url.URL{
		Path: "/ja/searchall_search/all/" + keyword + "/1.html",
	}).String()
}

func (m *MMTV) fetchPage(rawURL string) ([]byte, error) {
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
	return body, nil
}

func (m *MMTV) parseSearchResults(body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	type rankedResult struct {
		result *model.MovieSearchResult
		rank   int
	}
	best := make(map[string]rankedResult)
	order := make([]string, 0)
	doc.Find(".col-item .video").Each(func(_ int, item *goquery.Selection) {
		link := item.Find(".video-title a").First()
		if link.Length() == 0 {
			link = item.Find(".video-preview a").First()
		}
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		homepage := m.normalizeDetailURL(href)
		id, parseErr := m.ParseMovieIDFromURL(homepage)
		if parseErr != nil || !strings.EqualFold(id, keyword) {
			return
		}

		cover := firstAttr(item.Find(".video-preview img").First(),
			"data-src", "data-original", "src")
		cover = m.absoluteURL(cover)
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      id,
			Title:       cleanText(link.Text()),
			Provider:    m.Name(),
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(item.Find(".video-info .text-muted").First().Text()),
		}
		if result.Title == "" {
			result.Title = cleanText(firstAttr(link, "title"))
		}
		if !result.IsValid() {
			return
		}

		rank := contentRank(homepage)
		current, exists := best[id]
		if !exists {
			order = append(order, id)
		}
		if !exists || rank < current.rank {
			best[id] = rankedResult{result: result, rank: rank}
		}
	})

	results := make([]*model.MovieSearchResult, 0, len(best))
	for _, id := range order {
		if result, ok := best[id]; ok {
			results = append(results, result.result)
		}
	}
	return results, nil
}

func (m *MMTV) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Number:        id,
		Title:         cleanText(doc.Find(".fullvideo-title").First().Text()),
		Provider:      m.Name(),
		Homepage:      homepage,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	cover := doc.Find(".content_main_cover img").First()
	if cover.Length() == 0 {
		cover = doc.Find(`meta[property="og:image"]`)
	}
	info.CoverURL = m.absoluteURL(firstAttr(cover,
		"data-src", "data-original", "src", "content"))
	info.ThumbURL = info.CoverURL

	doc.Find(".fullvideo-details > .d-flex > span.text-muted").Each(func(_ int, meta *goquery.Selection) {
		value := cleanText(meta.Text())
		switch {
		case m.NormalizeMovieID(value) != "":
			info.Number = m.NormalizeMovieID(value)
		case runtimeRe.MatchString(value):
			match := runtimeRe.FindStringSubmatch(value)
			info.Runtime = parser.ParseInt(match[1])
		case !time.Time(parser.ParseDate(value)).IsZero():
			info.ReleaseDate = parser.ParseDate(value)
		}
	})

	doc.Find(".video-introduction-images-text p").Each(func(_ int, item *goquery.Selection) {
		summary := cleanText(item.Text())
		if summary == "" {
			return
		}
		if info.Summary != "" {
			info.Summary += "\n"
		}
		info.Summary += summary
	})
	doc.Find(".categories a").Each(func(_ int, item *goquery.Selection) {
		info.Genres = appendUnique(info.Genres, item.Text())
	})
	doc.Find(".fullvideo-idol span a").Each(func(_ int, item *goquery.Selection) {
		info.Actors = appendUnique(info.Actors, item.Text())
	})
	doc.Find(".fullvideo-attr .row").Each(func(_ int, row *goquery.Selection) {
		label := strings.Trim(cleanText(row.Find("strong").First().Text()), ":：")
		value := cleanText(row.Find("a").First().Text())
		if value == "" {
			value = cleanText(row.Find(".col-auto").Last().Text())
		}
		switch label {
		case "監督", "导演", "導演":
			info.Director = value
		case "メーカー", "片商":
			info.Maker = value
		case "レーベル", "發行商", "发行商", "標籤", "标签":
			info.Label = value
		case "シリーズ", "系列":
			info.Series = value
		}
	})
	doc.Find("#fullvideo_showallcg img").Each(func(_ int, item *goquery.Selection) {
		info.PreviewImages = appendUnique(info.PreviewImages,
			m.absoluteURL(firstAttr(item, "data-src", "data-original", "src")))
	})
	doc.Find("script").Each(func(_ int, script *goquery.Selection) {
		scriptText := script.Text()
		if !strings.Contains(scriptText, "fullvideo_showallcg_arr") {
			return
		}
		for _, match := range previewScriptRe.FindAllStringSubmatch(scriptText, -1) {
			if len(match) == 2 {
				info.PreviewImages = appendUnique(info.PreviewImages, m.absoluteURL(match[1]))
			}
		}
	})

	if info.Title == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (m *MMTV) isSiteHost(host string) bool {
	return strings.EqualFold(host, "7mmtv.sx") ||
		strings.EqualFold(host, "www.7mmtv.sx") ||
		strings.EqualFold(host, m.URL().Hostname())
}

func (m *MMTV) normalizeDetailURL(rawURL string) string {
	rawURL = m.absoluteURL(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || !m.isSiteHost(u.Hostname()) {
		return rawURL
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) > 0 {
		switch strings.ToLower(segments[0]) {
		case "en", "ja", "ko", "zh":
			segments[0] = "ja"
			u.Path = "/" + strings.Join(segments, "/")
		}
	}
	return u.String()
}

func (m *MMTV) absoluteURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return m.URL().ResolveReference(u).String()
}

func contentRank(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 4
	}
	switch {
	case strings.Contains(u.Path, "/censored_content/"):
		return 0
	case strings.Contains(u.Path, "/chinese_content/"):
		return 1
	case strings.Contains(u.Path, "/reducing-mosaic_content/"):
		return 3
	default:
		return 2
	}
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "<title>just a moment...</title>") ||
		strings.Contains(lower, "<title>attention required! | cloudflare</title>") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "__cf_chl_") ||
		strings.Contains(lower, "id=\"challenge-form\"") {
		return errAntiBotRequest
	}
	return nil
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
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
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
