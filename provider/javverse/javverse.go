package javverse

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"

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
	_ provider.MovieProvider = (*JavVerse)(nil)
	_ provider.MovieSearcher = (*JavVerse)(nil)
	_ provider.Fetcher       = (*JavVerse)(nil)

	labelNumberRe       = regexp.MustCompile(`(?i)^([a-z][a-z0-9]*?)[-_\s]?0*(\d+)$`)
	numericIDRe         = regexp.MustCompile(`^\d+$`)
	leadingCodeRe       = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9]*?)[-_\s]?0*(\d+)`)
	errAntiBotChallenge = errors.New("JavVerse returned a Cloudflare challenge")
	errAgeGateRequired  = errors.New("JavVerse age verification required")
)

const (
	Name     = "JavVerse"
	Priority = 1000 - 16
	baseURL  = "https://javverse.com/ja/"
)

type JavVerse struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavVerse {
	return &JavVerse{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			}),
			scraper.WithCookies(baseURL, []*http.Cookie{
				{Name: "jv-gate", Value: "1"},
			}),
		),
	}
}

func (jv *JavVerse) NormalizeMovieID(id string) string {
	id = cleanText(id)
	if strings.HasSuffix(strings.ToLower(id), ".html") {
		id = strings.TrimSuffix(id, path.Ext(id))
	}
	if numericIDRe.MatchString(id) {
		return id
	}
	return normalizeCode(id)
}

func (jv *JavVerse) NormalizeMovieKeyword(keyword string) string {
	keyword = cleanText(number.Trim(cleanText(keyword)))
	return jv.NormalizeMovieID(keyword)
}

func (jv *JavVerse) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !jv.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) >= 2 && segments[len(segments)-2] == "work" {
		if id := jv.NormalizeMovieID(segments[len(segments)-1]); numericIDRe.MatchString(id) {
			return id, nil
		}
	}
	if id := jv.NormalizeMovieKeyword(u.Query().Get("q")); id != "" {
		return id, nil
	}
	return "", provider.ErrInvalidURL
}

func (jv *JavVerse) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	rawID := cleanText(id)
	if numericIDRe.MatchString(rawID) {
		return jv.getMovieInfoByURL(jv.detailURL(rawID), rawID)
	}

	keyword := jv.NormalizeMovieKeyword(rawID)
	if keyword == "" {
		return nil, provider.ErrInvalidID
	}
	results, err := jv.SearchMovie(keyword)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return jv.getMovieInfoByURL(results[0].Homepage, results[0].ID)
}

func (jv *JavVerse) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := jv.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	if numericIDRe.MatchString(id) {
		return jv.getMovieInfoByURL(rawURL, id)
	}
	return jv.GetMovieInfoByID(id)
}

func (jv *JavVerse) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = jv.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	body, err := jv.fetchPage(jv.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	results, err := jv.parseSearchResults(body)
	if err != nil {
		return nil, err
	}
	filtered := jv.filterSearchResults(results, keyword)
	if len(filtered) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return filtered, nil
}

func (jv *JavVerse) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := jv.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return jv.parseMovieInfo(body, id, rawURL)
}

func (jv *JavVerse) fetchPage(rawURL string) ([]byte, error) {
	c := jv.ClonedCollector()

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

func (jv *JavVerse) parseSearchResults(body []byte) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	items := doc.Find("a.work-card")
	results := make([]*model.MovieSearchResult, 0, items.Length())
	items.Each(func(_ int, item *goquery.Selection) {
		href, ok := item.Attr("href")
		if !ok {
			return
		}
		homepage := jv.absoluteURL(href)
		id, parseErr := jv.ParseMovieIDFromURL(homepage)
		if parseErr != nil || !numericIDRe.MatchString(id) {
			return
		}

		number := normalizeCode(firstAttr(item.Find(".code-plate").First(), "aria-label"))
		if number == "" {
			number = codeFromText(firstAttr(item.Find("img").First(), "alt"))
		}
		title := cleanMovieTitle(item.Find("h3[lang=ja]").First().Text(), number)
		cover := jv.absoluteURL(firstAttr(item.Find("img").First(),
			"data-src", "data-original", "src"))
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      number,
			Title:       title,
			Provider:    Name,
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			Score:       parser.ParseScore(firstAttr(item.Find(".rating-display").First(), "aria-label")),
			ReleaseDate: parser.ParseDate(item.Find(".work-card-bottom").First().Text()),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) > 0 {
		return results, nil
	}
	if isNoResultsDocument(doc) {
		return nil, provider.ErrInfoNotFound
	}
	return nil, fmt.Errorf("%s: unrecognized search response", Name)
}

func (jv *JavVerse) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Title:         cleanText(doc.Find("h1[lang=ja]").First().Text()),
		Provider:      Name,
		Homepage:      homepage,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	doc.Find(".detail-facts dl > div").Each(func(_ int, row *goquery.Selection) {
		label := cleanText(row.Find("dt").First().Text())
		value := row.Find("dd").First()
		switch label {
		case "発売日":
			info.ReleaseDate = parser.ParseDate(value.Text())
		case "出演者":
			for _, actor := range factValues(value, true) {
				info.Actors = appendUnique(info.Actors, actor)
			}
		case "収録時間":
			info.Runtime = parser.ParseRuntime(value.Text())
		case "監督":
			info.Director = cleanText(value.Text())
		case "制作メーカー":
			info.Maker = cleanText(value.Text())
		case "レーベル":
			info.Label = cleanText(value.Text())
		case "シリーズ":
			if series := cleanText(value.Text()); !isUnregisteredValue(series) {
				info.Series = series
			}
		case "ジャンル":
			for _, genre := range factValues(value, true) {
				info.Genres = appendUnique(info.Genres, genre)
			}
		case "情報元の番号":
			info.Number = jv.NormalizeMovieKeyword(value.Text())
		}
	})

	if info.Number == "" {
		info.Number = codePlateNumber(doc)
	}
	if info.Number == "" {
		info.Number = codeFromText(firstAttr(doc.Find(".detail-cover img").First(), "alt"))
	}
	info.Title = cleanMovieTitle(info.Title, info.Number)

	cover := doc.Find(".detail-cover img").First()
	if cover.Length() == 0 {
		cover = doc.Find(`meta[property="og:image"]`).First()
	}
	info.CoverURL = jv.absoluteURL(firstAttr(cover,
		"data-src", "data-original", "src", "content"))
	info.ThumbURL = info.CoverURL
	info.Score = parser.ParseScore(firstAttr(
		doc.Find(".rating-display").First(), "aria-label"))

	doc.Find("section.work-stills a.still").Each(func(_ int, still *goquery.Selection) {
		image := jv.absoluteURL(firstAttr(still, "href", "data-src", "src"))
		if image == "" {
			image = jv.absoluteURL(firstAttr(still.Find("img").First(),
				"data-src", "data-original", "src"))
		}
		info.PreviewImages = appendUnique(info.PreviewImages, image)
	})

	if info.Number == "" || info.Title == "" {
		if isNoResultsDocument(doc) {
			return nil, provider.ErrInfoNotFound
		}
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (jv *JavVerse) filterSearchResults(results []*model.MovieSearchResult, keyword string) []*model.MovieSearchResult {
	filtered := make([]*model.MovieSearchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result == nil || jv.NormalizeMovieKeyword(result.Number) != keyword {
			continue
		}
		key := result.ID
		if key == "" {
			key = result.Homepage
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, result)
	}
	return filtered
}

func (jv *JavVerse) detailURL(id string) string {
	return jv.URL().ResolveReference(&url.URL{Path: "/ja/work/" + id}).String()
}

func (jv *JavVerse) searchURL(keyword string) string {
	query := url.Values{"q": {keyword}}
	return jv.URL().ResolveReference(&url.URL{
		Path:     "/ja/search",
		RawQuery: query.Encode(),
	}).String()
}

func (jv *JavVerse) absoluteURL(rawURL string) string {
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
	return jv.URL().ResolveReference(u).String()
}

func (jv *JavVerse) isSiteHost(host string) bool {
	return strings.EqualFold(host, "javverse.com") ||
		strings.EqualFold(host, "www.javverse.com") ||
		strings.EqualFold(host, jv.URL().Hostname())
}

func normalizeCode(value string) string {
	match := labelNumberRe.FindStringSubmatch(cleanText(value))
	if len(match) != 3 {
		return ""
	}
	digits := strings.TrimLeft(match[2], "0")
	if digits == "" {
		digits = "0"
	}
	return strings.ToUpper(match[1]) + "-" + digits
}

func codeFromText(value string) string {
	match := leadingCodeRe.FindStringSubmatch(cleanText(value))
	if len(match) != 3 {
		return ""
	}
	digits := strings.TrimLeft(match[2], "0")
	if digits == "" {
		digits = "0"
	}
	return strings.ToUpper(match[1]) + "-" + digits
}

func codePlateNumber(doc *goquery.Document) string {
	plate := doc.Find(".code-plate-large").First()
	if plate.Length() == 0 {
		plate = doc.Find(".code-plate").First()
	}
	if plate.Length() == 0 {
		return ""
	}
	if number := normalizeCode(firstAttr(plate, "aria-label")); number != "" {
		return number
	}

	prefix := cleanText(plate.Find(".code-plate-prefix").First().Text())
	numberSelection := plate.Find(".code-plate-number").First().Clone()
	numberSelection.Find(".sr-only").Remove()
	number := cleanText(numberSelection.Text())
	number = strings.TrimSpace(strings.TrimPrefix(number, prefix))
	return normalizeCode(prefix + number)
}

func factValues(value *goquery.Selection, splitLines bool) []string {
	values := make([]string, 0)
	value.Find("a").Each(func(_ int, item *goquery.Selection) {
		values = appendUnique(values, item.Text())
	})
	if len(values) > 0 {
		return values
	}

	raw := value.Text()
	if !splitLines {
		return appendUnique(values, raw)
	}
	for _, line := range strings.Split(raw, "\n") {
		for _, item := range strings.FieldsFunc(line, func(r rune) bool {
			return r == '\r' || r == '\t' || r == '、' || r == ',' ||
				r == ';' || r == '；'
		}) {
			values = appendUnique(values, item)
		}
	}
	return values
}

func isUnregisteredValue(value string) bool {
	switch strings.ToLower(cleanText(value)) {
	case "", "未登録", "未登录", "not registered", "unregistered", "-":
		return true
	default:
		return false
	}
}

func isNoResultsDocument(doc *goquery.Document) bool {
	if doc.Find(".search-group-empty").Length() > 0 {
		return true
	}
	return strings.Contains(doc.Text(), "関連作品は見つかりませんでした")
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	switch {
	case strings.Contains(lower, "just a moment"),
		strings.Contains(lower, `id="challenge-form"`),
		strings.Contains(lower, `id='challenge-form'`),
		strings.Contains(lower, "__cf_chl_"),
		strings.Contains(lower, "cf-browser-verification"),
		strings.Contains(lower, "attention required! | cloudflare"):
		return errAntiBotChallenge
	case strings.Contains(lower, "年齢確認") &&
		!strings.Contains(lower, "detail-cover") &&
		!strings.Contains(lower, "work-card"):
		return errAgeGateRequired
	default:
		return nil
	}
}

func cleanMovieTitle(title, number string) string {
	title = cleanText(title)
	number = cleanText(number)
	if title == "" || number == "" {
		return title
	}

	cleaned := title
	for len(cleaned) >= len(number) && strings.EqualFold(cleaned[:len(number)], number) {
		rest := cleaned[len(number):]
		if rest == "" {
			return title
		}
		trimmed := strings.TrimLeftFunc(rest, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("-:：|・—–", r)
		})
		if trimmed == rest {
			break
		}
		cleaned = trimmed
	}
	if cleaned == "" {
		return title
	}
	return cleaned
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
