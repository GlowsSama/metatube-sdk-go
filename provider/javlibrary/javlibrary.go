package javlibrary

import (
	"bytes"
	"errors"
	"fmt"
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
	_ provider.MovieProvider = (*JavLibrary)(nil)
	_ provider.MovieSearcher = (*JavLibrary)(nil)
	_ provider.Fetcher       = (*JavLibrary)(nil)

	labelNumberRe       = regexp.MustCompile(`(?i)^([a-z][a-z0-9]*?)[-_\s]?(\d+)$`)
	detailIDRe          = regexp.MustCompile(`(?i)^[a-z][a-z0-9]{5,}$`)
	numberIDRe          = regexp.MustCompile(`(?i)^[a-z0-9]+-\d+$`)
	scorePattern        = regexp.MustCompile(`\d+(?:\.\d+)?`)
	errAntiBotChallenge = errors.New("JavLibrary returned an anti-bot challenge")
)

const (
	Name     = "JavLibrary"
	Priority = 1000 - 12
	baseURL  = "https://www.javlibrary.com/ja/"
)

type JavLibrary struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavLibrary {
	return &JavLibrary{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			})),
	}
}

func (jl *JavLibrary) NormalizeMovieID(id string) string {
	id = cleanText(id)
	if strings.HasSuffix(strings.ToLower(id), ".html") {
		id = strings.TrimSuffix(id, path.Ext(id))
	}
	if detailIDRe.MatchString(id) {
		return strings.ToLower(id)
	}
	return strings.ToUpper(id)
}

func (jl *JavLibrary) NormalizeMovieKeyword(keyword string) string {
	keyword = cleanText(number.Trim(cleanText(keyword)))
	if keyword == "" {
		return ""
	}
	if match := labelNumberRe.FindStringSubmatch(keyword); len(match) == 3 {
		return strings.ToUpper(match[1]) + "-" + match[2]
	}
	return strings.ToUpper(keyword)
}

func (jl *JavLibrary) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !jl.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	switch path.Base(u.Path) {
	case "vl_searchbyid.php":
		if id := jl.NormalizeMovieKeyword(u.Query().Get("keyword")); id != "" {
			return id, nil
		}
	case "":
		return "", provider.ErrInvalidURL
	default:
		if strings.EqualFold(path.Ext(u.Path), ".html") {
			id := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
			if detailIDRe.MatchString(id) {
				return strings.ToLower(id), nil
			}
		}
	}
	return "", provider.ErrInvalidURL
}

func (jl *JavLibrary) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	rawID := cleanText(id)
	keyword := jl.NormalizeMovieKeyword(rawID)
	if numberIDRe.MatchString(keyword) {
		results, err := jl.SearchMovie(keyword)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, provider.ErrInfoNotFound
		}
		return jl.getMovieInfoByURL(results[0].Homepage, results[0].ID)
	}

	detailID := jl.NormalizeMovieID(rawID)
	if !detailIDRe.MatchString(detailID) {
		return nil, provider.ErrInvalidID
	}
	return jl.getMovieInfoByURL(jl.detailURL(detailID), detailID)
}

func (jl *JavLibrary) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := jl.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	if detailIDRe.MatchString(id) && !numberIDRe.MatchString(id) {
		return jl.getMovieInfoByURL(rawURL, id)
	}
	return jl.GetMovieInfoByID(id)
}

func (jl *JavLibrary) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = jl.NormalizeMovieKeyword(keyword)
	if keyword == "" || !numberIDRe.MatchString(keyword) {
		return nil, provider.ErrInvalidKeyword
	}
	body, finalURL, err := jl.fetchPage(jl.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	results, err := jl.parseSearchResults(body, finalURL)
	if err != nil {
		return nil, err
	}
	filtered := jl.filterSearchResults(results, keyword)
	if len(filtered) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return filtered, nil
}

func (jl *JavLibrary) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, finalURL, err := jl.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return jl.parseMovieInfo(body, id, finalURL)
}

func (jl *JavLibrary) fetchPage(rawURL string) ([]byte, string, error) {
	c := jl.ClonedCollector()

	var (
		body        []byte
		finalURL    = rawURL
		responseErr error
	)
	c.OnResponse(func(r *colly.Response) {
		body = append([]byte(nil), r.Body...)
		if r.Request != nil && r.Request.URL != nil {
			finalURL = r.Request.URL.String()
		}
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
			return nil, "", err
		}
	}
	if visitErr != nil {
		if responseErr != nil {
			return nil, "", responseErr
		}
		return nil, "", visitErr
	}
	if responseErr != nil {
		return nil, "", responseErr
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%s: empty response", Name)
	}
	return body, finalURL, nil
}

func (jl *JavLibrary) parseSearchResults(body []byte, homepage string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	if doc.Find("#video_id").Length() > 0 {
		id, parseErr := jl.ParseMovieIDFromURL(homepage)
		if parseErr != nil {
			return nil, parseErr
		}
		info, parseErr := jl.parseMovieInfo(body, id, homepage)
		if parseErr != nil {
			return nil, parseErr
		}
		return []*model.MovieSearchResult{info.ToSearchResult()}, nil
	}

	items := doc.Find(".video")
	results := make([]*model.MovieSearchResult, 0, items.Length())
	items.Each(func(_ int, item *goquery.Selection) {
		link := item.Find("a").First()
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		detailURL := jl.absoluteURL(href)
		id, parseErr := jl.ParseMovieIDFromURL(detailURL)
		if parseErr != nil {
			return
		}
		number := cleanText(item.Find(".id").First().Text())
		if number == "" {
			number = cleanText(link.AttrOr("title", ""))
		}
		title := cleanText(item.Find(".title").First().Text())
		if title == "" {
			title = cleanMovieTitle(link.AttrOr("title", ""), number)
		}
		cover := jl.absoluteURL(firstAttr(item.Find("img").First(), "data-src", "src"))
		result := &model.MovieSearchResult{
			ID:       id,
			Number:   jl.NormalizeMovieKeyword(number),
			Title:    cleanMovieTitle(title, number),
			Provider: Name,
			Homepage: detailURL,
			ThumbURL: cover,
			CoverURL: cover,
		}
		if result.IsValid() {
			results = append(results, result)
		}
	})
	if len(results) > 0 {
		return results, nil
	}
	if strings.Contains(doc.Text(), "ご指定の検索条件に合う項目がありませんでした") {
		return nil, provider.ErrInfoNotFound
	}
	return nil, fmt.Errorf("%s: unrecognized search response", Name)
}

func (jl *JavLibrary) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	info := &model.MovieInfo{
		ID:            id,
		Provider:      Name,
		Homepage:      homepage,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}
	info.Number = jl.NormalizeMovieKeyword(doc.Find("#video_id .text").First().Text())
	info.Title = cleanMovieTitle(doc.Find("#video_title h3").First().Text(), info.Number)
	info.ReleaseDate = parser.ParseDate(doc.Find("#video_date .text").First().Text())
	info.Runtime = parser.ParseRuntime(doc.Find("#video_length").First().Text())
	info.Director = cleanText(doc.Find("#video_director .text a").First().Text())
	info.Maker = cleanText(doc.Find("#video_maker .text a").First().Text())
	info.Label = cleanText(doc.Find("#video_label .text a").First().Text())
	info.Series = cleanText(doc.Find("#video_series .text a").First().Text())

	cover := doc.Find("#video_jacket_img").First()
	if cover.Length() == 0 {
		cover = doc.Find(`meta[property="og:image"]`).First()
	}
	info.CoverURL = jl.absoluteURL(firstAttr(cover, "data-src", "src", "content"))
	info.ThumbURL = info.CoverURL

	doc.Find("#video_genres a").Each(func(_ int, item *goquery.Selection) {
		info.Genres = appendUnique(info.Genres, item.Text())
	})
	doc.Find("#video_cast .star a").Each(func(_ int, item *goquery.Selection) {
		info.Actors = appendUnique(info.Actors, item.Text())
	})
	if match := scorePattern.FindString(doc.Find("#video_review .score").First().Text()); match != "" {
		info.Score = parser.ParseScore(match)
	}

	if info.Number == "" || info.Title == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (jl *JavLibrary) filterSearchResults(results []*model.MovieSearchResult, keyword string) []*model.MovieSearchResult {
	filtered := make([]*model.MovieSearchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result == nil || jl.NormalizeMovieKeyword(result.Number) != keyword {
			continue
		}
		key := result.ID
		if key == "" {
			key = jl.NormalizeMovieKeyword(result.Number)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, result)
	}
	return filtered
}

func (jl *JavLibrary) detailURL(id string) string {
	return jl.URL().ResolveReference(&url.URL{Path: "/ja/" + id + ".html"}).String()
}

func (jl *JavLibrary) searchURL(keyword string) string {
	query := url.Values{"keyword": {keyword}}
	return jl.URL().ResolveReference(&url.URL{
		Path:     "/ja/vl_searchbyid.php",
		RawQuery: query.Encode(),
	}).String()
}

func (jl *JavLibrary) absoluteURL(rawURL string) string {
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
	return jl.URL().ResolveReference(u).String()
}

func (jl *JavLibrary) isSiteHost(host string) bool {
	return strings.EqualFold(host, "javlibrary.com") ||
		strings.EqualFold(host, "www.javlibrary.com") ||
		strings.EqualFold(host, jl.URL().Hostname())
}

func detectPageError(body []byte) error {
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "正在執行安全驗證") ||
		strings.Contains(lower, "正在执行安全验证") ||
		strings.Contains(lower, "performing security verification") ||
		strings.Contains(lower, "cf-chl") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "__cf_chl_") ||
		strings.Contains(lower, `id="challenge-form"`) ||
		strings.Contains(lower, "just a moment") {
		return errAntiBotChallenge
	}
	return nil
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
