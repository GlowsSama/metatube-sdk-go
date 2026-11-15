package avfan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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
	"github.com/metatube-community/metatube-sdk-go/provider/internal/javnumber"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*AVFan)(nil)
	_ provider.MovieSearcher = (*AVFan)(nil)
	_ provider.Fetcher       = (*AVFan)(nil)

	slugRe      = regexp.MustCompile(`^[A-Za-z0-9]{6,64}$`)
	numberRe    = regexp.MustCompile(`(?i)^([a-z][a-z0-9]*)-0*(\d+)$`)
	dateTextRe  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	htmlBreakRe = regexp.MustCompile(`(?i)</?(?:br|p|div)[^>]*>`)
	htmlTagRe   = regexp.MustCompile(`(?s)<[^>]+>`)
)

const (
	Name     = "AVFan"
	Priority = 1000 - 15
	baseURL  = "https://avfan.com/ja/"
)

// AVFan provides metadata from AVFan's Japanese search and detail pages.
type AVFan struct {
	*fetch.Fetcher
	*scraper.Scraper
}

type videoObject struct {
	Type            string          `json:"@type"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	ThumbnailURL    string          `json:"thumbnailUrl"`
	URL             string          `json:"url"`
	ContentURL      string          `json:"contentUrl"`
	UploadDate      string          `json:"uploadDate"`
	Duration        string          `json:"duration"`
	AggregateRating aggregateRating `json:"aggregateRating"`
}

type aggregateRating struct {
	RatingValue float64 `json:"ratingValue"`
}

func New() *AVFan {
	return &AVFan{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
			}),
		),
	}
}

func (a *AVFan) NormalizeMovieID(id string) string {
	raw := cleanText(id)
	if normalized := javnumber.Normalize(raw); normalized != "" {
		return normalized
	}
	if slugRe.MatchString(raw) {
		return raw
	}
	return ""
}

func (a *AVFan) NormalizeMovieKeyword(keyword string) string {
	return canonicalSearchNumber(javnumber.Normalize(number.Trim(cleanText(keyword))))
}

func (a *AVFan) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !a.isSiteHost(u.Hostname()) {
		return "", provider.ErrInvalidURL
	}

	segments := splitPath(u.Path)
	for i := 0; i+1 < len(segments); i++ {
		if !strings.EqualFold(segments[i], "movies") {
			continue
		}
		if id := a.NormalizeMovieID(segments[i+1]); id != "" && slugRe.MatchString(id) {
			return id, nil
		}
	}
	return "", provider.ErrInvalidURL
}

func (a *AVFan) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = a.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	if slugRe.MatchString(id) {
		return a.getMovieInfoByURL(a.detailURL(id), id)
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

func (a *AVFan) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := a.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return a.getMovieInfoByURL(rawURL, id)
}

func (a *AVFan) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
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

func (a *AVFan) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	body, err := a.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return a.parseMovieInfo(body, id, rawURL)
}

func (a *AVFan) searchURL(keyword string) string {
	return a.URL().ResolveReference(&url.URL{
		Path: "/ja/search",
		RawQuery: url.Values{
			"q":  {keyword},
			"st": {"movie"},
		}.Encode(),
	}).String()
}

func (a *AVFan) detailURL(id string) string {
	return a.URL().ResolveReference(&url.URL{
		Path: "/ja/movies/" + id,
	}).String()
}

func (a *AVFan) fetchPage(rawURL string) ([]byte, error) {
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

func (a *AVFan) parseSearchResults(body []byte, keyword string) ([]*model.MovieSearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse search response: %w", Name, err)
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	doc.Find("div.flex.flex-col.relative.group").Each(func(_ int, card *goquery.Selection) {
		codeNode := card.Find("div.title strong.history-link").First()
		if codeNode.Length() == 0 {
			return
		}
		number := javnumber.Normalize(codeNode.Text())
		if number == "" || !strings.EqualFold(canonicalSearchNumber(number), keyword) {
			return
		}

		link := codeNode.Closest("a")
		if link.Length() == 0 {
			link = card.Find("a[href*='/movies/']").First()
		}
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		homepage := a.absoluteURL(href)
		id, parseErr := a.ParseMovieIDFromURL(homepage)
		if parseErr != nil {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		title := firstText(card.Find("div.title span").First())
		if title == "" {
			title = firstAttr(link, "title")
		}
		title = cleanSearchTitle(title, number)

		cover := a.absoluteURL(firstAttr(card.Find("img").First(),
			"data-src", "data-original", "src"))
		score := parser.ParseScore(firstAttr(card.Find("span[title*='点']").First(), "title"))
		releaseDate := parser.ParseDate(dateTextRe.FindString(cleanText(card.Text())))
		result := &model.MovieSearchResult{
			ID:          id,
			Number:      number,
			Title:       title,
			Provider:    Name,
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			Score:       score,
			ReleaseDate: releaseDate,
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

func (a *AVFan) parseMovieInfo(body []byte, id, homepage string) (*model.MovieInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse detail response: %w", Name, err)
	}

	video := parseVideoObject(doc)
	info := &model.MovieInfo{
		ID:              id,
		Title:           firstText(doc.Find(`h1[data-movie-target="title"]`).First()),
		Summary:         cleanHTML(video.Description),
		Provider:        Name,
		Homepage:        homepage,
		PreviewVideoURL: strings.TrimSpace(video.ContentURL),
		Actors:          []string{},
		PreviewImages:   []string{},
		Genres:          []string{},
	}
	if info.Title == "" {
		info.Title = video.Name
	}

	doc.Find("dl.movie-info > dt, dl.movie-info > dd").Each(func(_ int, node *goquery.Selection) {
		if node.Is("dt") {
			return
		}
		label := strings.ToLower(firstText(node.Prev()))
		switch label {
		case "number":
			info.Number = normalizeNumberValue(node)
		case "released date", "release date", "発売日":
			info.ReleaseDate = parser.ParseDate(firstText(node))
		case "duration", "収録時間":
			info.Runtime = parser.ParseRuntime(firstText(node))
		case "maker", "メーカー":
			info.Maker = firstText(node.Find("a").First())
		case "publisher", "レーベル":
			info.Label = firstText(node.Find("a").First())
		case "director", "監督":
			info.Director = firstText(node.Find("a").First())
		}
	})

	doc.Find(".movie-info a[href*='/casts/']").Each(func(_ int, actor *goquery.Selection) {
		info.Actors = appendUnique(info.Actors, actor.Text())
	})
	doc.Find(".movie-info a.tag-chip").Each(func(_ int, genre *goquery.Selection) {
		info.Genres = appendUnique(info.Genres, genre.Text())
	})
	doc.Find(`a[data-fancybox="gallery"][href*="/samples/"]`).Each(func(_ int, sample *goquery.Selection) {
		info.PreviewImages = appendUnique(info.PreviewImages, a.absoluteURL(firstAttr(sample, "href")))
	})

	if info.Number == "" {
		info.Number = normalizeNumberValue(doc.Find("dl.movie-info").First())
	}
	if info.Number == "" {
		info.Number = javnumber.Find(info.Title)
	}
	info.Title = cleanTitle(info.Title, info.Number)

	info.CoverURL = a.absoluteURL(firstAttr(
		doc.Find(`img[data-movie-target="cover"]`).First(), "src", "data-src"))
	if info.CoverURL == "" {
		info.CoverURL = a.absoluteURL(video.ThumbnailURL)
	}
	if info.CoverURL == "" {
		info.CoverURL = a.absoluteURL(firstAttr(doc.Find(`meta[property="og:image"]`).First(), "content"))
	}
	info.ThumbURL = info.CoverURL

	if info.Score == 0 {
		info.Score = video.AggregateRating.RatingValue
	}
	if time.Time(info.ReleaseDate).IsZero() {
		info.ReleaseDate = parser.ParseDate(video.UploadDate)
	}
	if info.Runtime == 0 {
		info.Runtime = parser.ParseRuntime(video.Duration)
	}

	if info.ID == "" || info.Number == "" || info.Title == "" {
		return nil, fmt.Errorf("%s: unrecognized detail response", Name)
	}
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func parseVideoObject(doc *goquery.Document) videoObject {
	var video videoObject
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, script *goquery.Selection) bool {
		var candidate videoObject
		if err := json.Unmarshal([]byte(script.Text()), &candidate); err != nil {
			return true
		}
		if strings.EqualFold(candidate.Type, "VideoObject") {
			video = candidate
			return false
		}
		return true
	})
	return video
}

func normalizeNumberValue(selection *goquery.Selection) string {
	if selection.Length() == 0 {
		return ""
	}
	if button := selection.Find("button[data-number]").First(); button.Length() > 0 {
		if number := javnumber.Normalize(firstAttr(button, "data-number")); number != "" {
			return number
		}
	}
	if marker := selection.Find(".movie-number").First(); marker.Length() > 0 {
		if number := javnumber.Normalize(marker.Text()); number != "" {
			return number
		}
	}
	return javnumber.Normalize(selection.Text())
}

func cleanSearchTitle(title, number string) string {
	title = cleanText(title)
	if title == "" {
		return number
	}
	prefix := "[" + number + "]"
	if len(title) >= len(prefix) && strings.EqualFold(title[:len(prefix)], prefix) {
		title = strings.TrimSpace(title[len(prefix):])
	}
	title = cleanTitle(title, number)
	if title == "" {
		return number
	}
	return title
}

func cleanTitle(title, number string) string {
	cleaned := cleanText(title)
	code := cleanText(number)
	if cleaned == "" || code == "" {
		return cleaned
	}
	for len(cleaned) >= len(code) && strings.EqualFold(cleaned[:len(code)], code) {
		rest := cleaned[len(code):]
		trimmed := strings.TrimLeftFunc(rest, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
				strings.ContainsRune("-:：|・—–", r)
		})
		if trimmed == rest {
			break
		}
		cleaned = trimmed
	}
	if cleaned == "" {
		return cleanText(title)
	}
	return cleaned
}

func canonicalSearchNumber(number string) string {
	match := numberRe.FindStringSubmatch(strings.TrimSpace(number))
	if len(match) != 3 {
		return number
	}
	return strings.ToUpper(match[1]) + "-" + match[2]
}

func cleanHTML(value string) string {
	value = htmlBreakRe.ReplaceAllString(value, "\n")
	value = htmlTagRe.ReplaceAllString(value, "")
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div>" + value + "</div>"))
	if err != nil {
		return cleanText(value)
	}
	return cleanText(doc.Text())
}

func (a *AVFan) absoluteURL(rawURL string) string {
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

func (a *AVFan) isSiteHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, candidate := range []string{a.URL().Hostname(), "avfan.com"} {
		candidate = strings.TrimPrefix(strings.ToLower(candidate), "www.")
		if host == candidate || host == "www."+candidate {
			return true
		}
	}
	return false
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
