package javdb

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/fc2/fc2util"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*JavDB)(nil)
	_ provider.MovieSearcher = (*JavDB)(nil)
	_ provider.Fetcher       = (*JavDB)(nil)

	scorePattern        = regexp.MustCompile(`\d+(?:\.\d+)?`)
	idPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	upperNumberPattern  = regexp.MustCompile(`^[A-Z]{1,10}\d{2,}$`)
	numericPattern      = regexp.MustCompile(`^\d+$`)
	fc2AttemptPattern   = regexp.MustCompile(`(?i)^FC2(?:[-_\s]?PPV)?[-_\s]?\d`)
	errLoginRequired    = errors.New("JavDB login required")
	errGeoRestricted    = errors.New("JavDB is unavailable in the current network region")
	errAntiBotChallenge = errors.New("JavDB returned an anti-bot challenge")
)

const (
	Name     = "JavDB"
	Priority = 1000 - 10
	baseURL  = "https://javdb.com/"
)

// JavDB provides public search and detail-page metadata. Detail-page IDs are
// JavDB's opaque slugs and must retain their original case. FC2 numbers remain
// numeric for backward compatibility and use the public search-card fallback.
type JavDB struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavDB {
	return &JavDB{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Chinese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "zh-CN,zh;q=0.9",
			})),
	}
}

func (jav *JavDB) NormalizeMovieID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if fc2AttemptPattern.MatchString(id) {
		return fc2util.ParseNumber(id)
	}
	if !idPattern.MatchString(id) {
		return ""
	}
	if hasMixedCase(id) {
		return id
	}
	if isNumberLike(id) {
		return strings.ToUpper(id)
	}
	return id
}

func (jav *JavDB) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") ||
		(!strings.EqualFold(u.Hostname(), "javdb.com") &&
			!strings.EqualFold(u.Hostname(), "www.javdb.com")) {
		return "", provider.ErrInvalidURL
	}

	switch {
	case strings.HasPrefix(u.Path, "/v/"):
		id, ok := detailSlugFromPath(u.Path)
		if !ok {
			return "", provider.ErrInvalidURL
		}
		return id, nil
	case u.Path == "/search":
		id := jav.NormalizeMovieKeyword(u.Query().Get("q"))
		if id == "" {
			return "", provider.ErrInvalidURL
		}
		return id, nil
	default:
		return "", provider.ErrInvalidURL
	}
}

func (jav *JavDB) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	id, err := jav.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	if _, ok := detailSlugFromPath(u.Path); ok {
		return jav.getMovieInfoByDetailURL(u.String(), id)
	}
	return jav.GetMovieInfoByID(id)
}

func (jav *JavDB) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = jav.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	if isNumberLike(id) {
		return jav.getMovieInfoByNumber(id)
	}
	return jav.getMovieInfoByDetailURL(jav.detailURL(id), id)
}

func (jav *JavDB) NormalizeMovieKeyword(keyword string) string {
	keyword = strings.TrimSpace(number.Trim(keyword))
	if keyword == "" {
		return ""
	}
	if fc2ID := fc2util.ParseNumber(keyword); fc2ID != "" {
		return "FC2-" + fc2ID
	}
	keyword = strings.ToUpper(keyword)
	if numericPattern.MatchString(keyword) {
		return "FC2-" + keyword
	}
	if !idPattern.MatchString(keyword) {
		return ""
	}
	return keyword
}

func (jav *JavDB) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = jav.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	results, err := jav.searchMovie(keyword)
	if err != nil {
		return nil, err
	}
	return jav.filterSearchResults(results, keyword), nil
}

func (jav *JavDB) searchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	body, err := jav.fetchPage(jav.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	return jav.parseSearchResults(body)
}

func (jav *JavDB) filterSearchResults(results []*model.MovieSearchResult, keyword string) []*model.MovieSearchResult {
	filtered := make([]*model.MovieSearchResult, 0, len(results))
	for _, result := range results {
		if result == nil || !strings.EqualFold(jav.NormalizeMovieKeyword(result.Number), keyword) {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func (jav *JavDB) getMovieInfoByNumber(n string) (*model.MovieInfo, error) {
	keyword := jav.NormalizeMovieKeyword(n)
	if keyword == "" {
		return nil, provider.ErrInvalidID
	}
	results, err := jav.searchMovie(keyword)
	if err != nil {
		return nil, err
	}

	filtered := jav.filterSearchResults(results, keyword)
	if len(filtered) == 0 {
		return nil, provider.ErrInfoNotFound
	}

	result := filtered[0]
	// FC2 detail pages often require a login. Keep using the complete public
	// search card for the legacy FC2 path while normal titles get full details.
	if fc2ID := fc2util.ParseNumber(result.Number); fc2ID != "" {
		return searchResultToInfo(result), nil
	}
	return jav.getMovieInfoByDetailURL(result.Homepage, result.ID)
}

func (jav *JavDB) getMovieInfoByDetailURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := jav.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return jav.parseMovieInfo(body, id, rawURL)
}

func (jav *JavDB) fetchPage(rawURL string) ([]byte, error) {
	c := jav.ClonedCollector()

	var (
		body        []byte
		responseErr error
	)
	c.OnResponse(func(r *colly.Response) {
		body = append([]byte(nil), r.Body...)
	})
	c.OnError(func(r *colly.Response, err error) {
		if len(r.Body) > 0 {
			body = append([]byte(nil), r.Body...)
		}
		responseErr = fmt.Errorf("%s: request failed with HTTP %d: %w", jav.Name(), r.StatusCode, err)
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
		return nil, fmt.Errorf("%s: empty response", jav.Name())
	}
	return body, nil
}

func (jav *JavDB) parseSearchResults(body []byte) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", jav.Name(), err)
	}

	items := doc.Find(".movie-list .item")
	if items.Length() == 0 {
		items = doc.Find("div.item")
	}
	results := make([]*model.MovieSearchResult, 0, items.Length())
	items.Each(func(_ int, item *goquery.Selection) {
		href, ok := item.Find("a.box").First().Attr("href")
		if !ok {
			return
		}
		id := jav.movieIDFromPath(href)
		if id == "" {
			return
		}

		titleSelection := item.Find(".video-title").First()
		number := normalizeNumber(titleSelection.Find("strong").First().Text())
		title := strings.TrimSpace(titleSelection.Find("strong").First().Text())
		if title != "" {
			rawTitle := strings.TrimSpace(titleSelection.Text())
			if strings.HasPrefix(rawTitle, title) {
				title = strings.TrimSpace(strings.TrimPrefix(rawTitle, title))
			}
		}
		if title == "" {
			title = strings.TrimSpace(item.Find("a.box").First().AttrOr("title", ""))
		}
		if number == "" || title == "" {
			return
		}

		cover := jav.absoluteURL(firstAttr(item.Find(".cover img").First(),
			"data-src", "data-original", "src"))
		resultID := id
		if fc2ID := fc2util.ParseNumber(number); fc2ID != "" {
			resultID = fc2ID
		}
		result := &model.MovieSearchResult{
			ID:          resultID,
			Number:      number,
			Title:       title,
			Provider:    jav.Name(),
			Homepage:    jav.absoluteURL(href),
			ThumbURL:    cover,
			CoverURL:    cover,
			Score:       parseScore(item.Find(".score").Text()),
			ReleaseDate: parser.ParseDate(item.Find(".meta").Text()),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) > 0 {
		return results, nil
	}
	if isLoginDocument(doc) {
		return nil, errLoginRequired
	}
	if hasNoResults(doc) {
		return nil, provider.ErrInfoNotFound
	}
	return nil, fmt.Errorf("%s: unrecognized search response", jav.Name())
}

func (jav *JavDB) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", jav.Name(), err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Provider:      jav.Name(),
		Homepage:      homepage,
		Title:         cleanText(doc.Find(".current-title").First().Text()),
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	coverSelection := doc.Find("img.video-cover, .video-cover img, .column-video-cover img").First()
	info.CoverURL = jav.absoluteURL(firstAttr(coverSelection,
		"data-src", "data-original", "src"))
	info.ThumbURL = info.CoverURL

	doc.Find(".movie-panel-info .panel-block, nav.movie-panel-info > div").Each(func(_ int, block *goquery.Selection) {
		label := cleanLabel(block.Find("strong").First().Text())
		value := block.Find(".value").First()
		if value.Length() == 0 {
			value = block.Clone()
			value.Find("strong").Remove()
		}

		switch label {
		case "番號", "番号", "ID":
			if raw, ok := value.Find("[data-clipboard-text]").First().Attr("data-clipboard-text"); ok {
				info.Number = normalizeNumber(raw)
			} else {
				info.Number = normalizeNumber(value.Text())
			}
		case "日期", "發行日期", "发行日期":
			info.ReleaseDate = parser.ParseDate(value.Text())
		case "時長", "时长":
			info.Runtime = parser.ParseRuntime(value.Text())
		case "評分", "评分":
			info.Score = parseScore(value.Text())
		case "導演", "导演":
			info.Director = cleanText(value.Find("a").First().Text())
			if info.Director == "" {
				info.Director = cleanText(value.Text())
			}
		case "片商":
			info.Maker = cleanText(value.Find("a").First().Text())
			if info.Maker == "" {
				info.Maker = cleanText(value.Text())
			}
		case "發行商", "发行商", "標籤", "标签":
			info.Label = cleanText(value.Find("a").First().Text())
			if info.Label == "" {
				info.Label = cleanText(value.Text())
			}
		case "系列":
			info.Series = cleanText(value.Find("a").First().Text())
			if info.Series == "" {
				info.Series = cleanText(value.Text())
			}
		case "類別", "类别", "類型", "类型":
			value.Find("a").Each(func(_ int, item *goquery.Selection) {
				info.Genres = appendUnique(info.Genres, cleanText(item.Text()))
			})
		case "演員", "演员":
			value.Find("a").Each(func(_ int, item *goquery.Selection) {
				info.Actors = appendUnique(info.Actors, cleanText(item.Text()))
			})
		}
	})

	doc.Find(".preview-images .tile-item, .tile-images.preview-images a").Each(func(_ int, image *goquery.Selection) {
		preview := jav.absoluteURL(firstAttr(image, "href", "data-src", "src"))
		info.PreviewImages = appendUnique(info.PreviewImages, preview)
	})

	if info.Title == "" || info.Number == "" {
		if isLoginDocument(doc) {
			return nil, errLoginRequired
		}
		return nil, fmt.Errorf("%s: unrecognized detail response", jav.Name())
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (jav *JavDB) movieIDFromPath(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	id, _ := detailSlugFromPath(u.Path)
	return id
}

func (jav *JavDB) detailURL(id string) string {
	return jav.URL().ResolveReference(&url.URL{Path: "/v/" + id}).String()
}

func (jav *JavDB) searchURL(keyword string) string {
	query := url.Values{
		"q":      {keyword},
		"f":      {"all"},
		"locale": {"zh"},
	}
	return jav.URL().ResolveReference(&url.URL{Path: "/search", RawQuery: query.Encode()}).String()
}

func (jav *JavDB) absoluteURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return jav.URL().ResolveReference(u).String()
}

func isNumberLike(id string) bool {
	return numericPattern.MatchString(id) ||
		strings.ContainsAny(id, "-_") ||
		upperNumberPattern.MatchString(id)
}

func detailSlugFromPath(urlPath string) (string, bool) {
	const prefix = "/v/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", false
	}
	slug := strings.TrimPrefix(urlPath, prefix)
	if slug == "" || strings.Contains(slug, "/") || !idPattern.MatchString(slug) {
		return "", false
	}
	return slug, true
}

func hasMixedCase(value string) bool {
	return strings.ToLower(value) != value && strings.ToUpper(value) != value
}

func normalizeNumber(raw string) string {
	raw = cleanText(raw)
	if raw == "" {
		return ""
	}
	if fc2ID := fc2util.ParseNumber(raw); fc2ID != "" {
		return "FC2-" + fc2ID
	}
	return strings.ToUpper(raw)
}

func parseScore(raw string) float64 {
	match := scorePattern.FindString(strings.TrimSpace(raw))
	score, _ := strconv.ParseFloat(match, 64)
	return score
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	switch {
	case strings.Contains(lower, "access to this site is prohibited in the country"):
		return fmt.Errorf("%w: copyright restriction", errGeoRestricted)
	case strings.Contains(lower, "cf-chl"),
		strings.Contains(lower, "challenge-platform"),
		strings.Contains(lower, "just a moment"):
		return errAntiBotChallenge
	default:
		return nil
	}
}

func isLoginDocument(doc *goquery.Document) bool {
	title := strings.ToLower(cleanText(doc.Find("title").First().Text()))
	return doc.Find(`form[action*="/login"], form[action*="sign_in"]`).Length() > 0 ||
		strings.Contains(title, "登入") ||
		strings.Contains(title, "登录") ||
		strings.Contains(title, "login")
}

func hasNoResults(doc *goquery.Document) bool {
	return doc.Find(".empty-message, .no-result, .null, .notification.is-danger").Length() > 0
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

func cleanLabel(value string) string {
	return strings.Trim(strings.TrimSpace(value), ":：")
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

func searchResultToInfo(result *model.MovieSearchResult) *model.MovieInfo {
	return &model.MovieInfo{
		ID:            result.ID,
		Number:        result.Number,
		Title:         result.Title,
		Provider:      result.Provider,
		Homepage:      result.Homepage,
		ThumbURL:      result.ThumbURL,
		CoverURL:      result.CoverURL,
		Score:         result.Score,
		ReleaseDate:   result.ReleaseDate,
		Actors:        []string{},
		Genres:        []string{},
		PreviewImages: []string{},
	}
}

func init() {
	provider.Register(Name, New)
}
