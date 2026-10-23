package av123

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/javnumber"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*AV123)(nil)
	_ provider.MovieSearcher = (*AV123)(nil)
	_ provider.Fetcher       = (*AV123)(nil)

	variantSlugRe       = regexp.MustCompile(`(?i)-(?:uncensored(?:-leak(?:ed)?)?|leak|english-subtitle|chinese-subtitle|japanese-subtitle|subtitle)$`)
	backgroundImageRe   = regexp.MustCompile(`(?i)background-image\s*:\s*url\(\s*['"]?([^'")]+)`)
	errAntiBotChallenge = errors.New("123AV returned a Cloudflare challenge")
)

const (
	Name     = "123AV"
	Priority = 1000 - 20
	baseURL  = "https://123av.com/ja/"
)

// AV123 provides metadata from 123AV's Japanese search and detail pages.
type AV123 struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *AV123 {
	return &AV123{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			}),
		),
	}
}

func (a *AV123) NormalizeMovieID(id string) string {
	return javnumber.Normalize(cleanText(id))
}

func (a *AV123) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !a.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	segments := splitPath(u.Path)
	for i := 0; i+2 < len(segments); i++ {
		if !strings.EqualFold(segments[i], "ja") ||
			!strings.EqualFold(segments[i+1], "v") {
			continue
		}
		slug := strings.TrimSuffix(segments[i+2], path.Ext(segments[i+2]))
		if slug == "" || variantSlugRe.MatchString(slug) {
			return "", provider.ErrInvalidURL
		}
		if id := a.NormalizeMovieID(slug); id != "" {
			return id, nil
		}
	}
	return "", provider.ErrInvalidURL
}

func (a *AV123) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = a.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	results, err := a.SearchMovie(id)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return a.getMovieInfoByURL(results[0].Homepage, results[0].ID)
}

func (a *AV123) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := a.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return a.getMovieInfoByURL(rawURL, id)
}

func (a *AV123) NormalizeMovieKeyword(keyword string) string {
	return a.NormalizeMovieID(number.Trim(cleanText(keyword)))
}

func (a *AV123) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = a.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	body, err := a.fetchPage(a.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	return a.parseSearchResults(body, keyword)
}

func (a *AV123) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := a.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return a.parseMovieInfo(body, id, rawURL)
}

func (a *AV123) searchURL(keyword string) string {
	return a.URL().ResolveReference(&url.URL{
		Path:     "/ja/search",
		RawQuery: url.Values{"keyword": {keyword}}.Encode(),
	}).String()
}

func (a *AV123) fetchPage(rawURL string) ([]byte, error) {
	c := a.ClonedCollector()

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

func (a *AV123) parseSearchResults(body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	doc.Find(".card").Each(func(_ int, item *goquery.Selection) {
		link := item.Find("a.card__cover").First()
		if link.Length() == 0 {
			link = item.Find("a[href*='/ja/v/']").First()
		}
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		homepage := a.absoluteURL(href)
		id, parseErr := a.ParseMovieIDFromURL(homepage)
		if parseErr != nil || !strings.EqualFold(id, keyword) {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		title := firstText(item.Find("h3.card__title a.card__link").First())
		if title == "" {
			title = firstAttr(link, "title")
		}
		if title == "" {
			title = id
		}
		cover := a.absoluteURL(firstAttr(item.Find("img.card__img").First(),
			"data-src", "data-original", "src"))
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      id,
			Title:       javnumber.CleanTitle(title, id),
			Provider:    Name,
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(firstText(item.Find(".card__date, time").First())),
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

func (a *AV123) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Title:         firstText(doc.Find("h1.watch__title").First()),
		Provider:      Name,
		Homepage:      homepage,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}
	if info.Title == "" {
		info.Title = firstText(doc.Find("h1").First())
	}

	doc.Find("dl.watch__info .watch__info-row").Each(func(_ int, row *goquery.Selection) {
		label := firstText(row.Find("dt").First())
		value := row.Find("dd").First()
		switch label {
		case "コード":
			info.Number = javnumber.Normalize(firstText(value))
		case "発売日":
			info.ReleaseDate = parser.ParseDate(firstText(value))
		case "出演者":
			for _, actor := range factValues(value) {
				info.Actors = appendUnique(info.Actors, actor)
			}
		case "メーカー":
			info.Maker = cleanText(value.Find(".chip, a").First().Text())
			if info.Maker == "" {
				info.Maker = cleanText(value.Text())
			}
		case "ジャンル", "タグ":
			for _, genre := range factValues(value) {
				info.Genres = appendUnique(info.Genres, genre)
			}
		case "監督":
			info.Director = firstText(value.Find(".chip, a").First())
		case "レーベル":
			info.Label = firstText(value.Find(".chip, a").First())
		case "シリーズ":
			info.Series = firstText(value.Find(".chip, a").First())
		case "収録時間":
			info.Runtime = parser.ParseRuntime(firstText(value))
		}
	})

	doc.Find("div.player").EachWithBreak(func(_ int, player *goquery.Selection) bool {
		style, _ := player.Attr("style")
		match := backgroundImageRe.FindStringSubmatch(style)
		if len(match) != 2 {
			return true
		}
		info.CoverURL = a.absoluteURL(match[1])
		info.ThumbURL = info.CoverURL
		return info.CoverURL == ""
	})

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

func (a *AV123) isSiteHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, candidate := range []string{a.URL().Hostname(), "123av.com"} {
		candidate = strings.TrimPrefix(strings.ToLower(candidate), "www.")
		if host == candidate || host == "www."+candidate {
			return true
		}
	}
	return false
}

func (a *AV123) absoluteURL(rawURL string) string {
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
	return a.URL().ResolveReference(u).String()
}

func factValues(value *goquery.Selection) []string {
	values := make([]string, 0)
	value.Find(".chip, a").Each(func(_ int, item *goquery.Selection) {
		values = appendUnique(values, item.Text())
	})
	if len(values) > 0 {
		return values
	}
	return appendUnique(values, value.Text())
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
