package manko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	apiHostRe           = regexp.MustCompile(`(?i)window\.(?:ip|ip2)\s*=\s*['"]([^'"]+)['"]`)
	variantSuffixRe     = regexp.MustCompile(`(?i)(?:^|[-_\s])(?:uncensored(?:[-_\s]leak(?:ed)?)?|leak(?:ed)?|english[-_\s]subtitle|chinese[-_\s]subtitle|japanese[-_\s]subtitle|subtitle|sub)$`)
	emptyAppShellRe     = regexp.MustCompile(`(?is)<div[^>]*\bid\s*=\s*["']app["'][^>]*>\s*</div>`)
	errAntiBotChallenge = errors.New("Manko returned a Cloudflare challenge")
)

const (
	Name             = "Manko"
	Priority         = 1000 - 21
	baseURL          = "https://manko.fun/"
	apiResponseLimit = 16 << 20
	apiBase85Chars   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&()*+,./:;<=>?@[]^_`{|}~\""
)

// Manko provides metadata from Manko's search and detail pages.
type Manko struct {
	*fetch.Fetcher
	*scraper.Scraper

	flareMu     sync.RWMutex
	flareClient *flaresolverr.Client

	apiMu    sync.RWMutex
	apiHosts []string
}

type mankoAPIEnvelope struct {
	Data string `json:"data"`
}

type mankoAPILocalizedTitle struct {
	Language string `json:"language"`
	Title    string `json:"title"`
}

type mankoAPILocalizedText struct {
	Language string `json:"language"`
	Overview string `json:"overview"`
}

type mankoAPIMovie struct {
	ID               string                   `json:"_id"`
	Title            string                   `json:"title"`
	Titles           []mankoAPILocalizedTitle `json:"title_display_name"`
	Descriptions     []mankoAPILocalizedText  `json:"discription"`
	Tags             []string                 `json:"tags"`
	Maker            string                   `json:"maker"`
	Country          string                   `json:"country"`
	ReleaseDate      string                   `json:"share_date"`
	Rating           string                   `json:"rating"`
	IMDbScore        string                   `json:"imdb_score"`
	VideoDuration    int                      `json:"video_duration"`
	ThumbnailImage   string                   `json:"thumbnail_image"`
	WideThumbnail    string                   `json:"wide_thumbnail"`
	CoverImage       string                   `json:"cover_image"`
	BackdropImages   []string                 `json:"backdrop_images"`
	PreviewImageURLS []string                 `json:"preview_images"`
}

type mankoAPIActor struct {
	Name        string            `json:"name"`
	AlsoKnownAs map[string]string `json:"also_known_as"`
}

func New() *Manko {
	return &Manko{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "ja-JP,ja;q=0.9",
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

	apiResults, apiErr := m.searchAPIMovie(keyword)
	if apiErr == nil {
		return apiResults, nil
	}

	body, err := m.fetchPage(m.searchURL(keyword))
	if err != nil {
		return nil, err
	}
	results, err := m.parseSearchResults(body, keyword)
	if err != nil && apiErr != nil {
		return nil, fmt.Errorf("%s: API fallback failed: %v; page parser failed: %w", Name, apiErr, err)
	}
	return results, err
}

func (m *Manko) getMovieInfoByURL(rawURL, id string) (*model.MovieInfo, error) {
	apiInfo, apiErr := m.getAPIMovieInfo(id, rawURL)
	if apiErr == nil {
		return apiInfo, nil
	}

	body, err := m.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	info, err := m.parseMovieInfo(body, id, rawURL)
	if err != nil && apiErr != nil {
		return nil, fmt.Errorf("%s: API fallback failed: %v; page parser failed: %w", Name, apiErr, err)
	}
	return info, err
}

func (m *Manko) searchAPIMovie(keyword string) ([]*model.MovieSearchResult, error) {
	movies, err := m.fetchAPIMovies(keyword)
	if err != nil {
		return nil, err
	}

	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	for _, movie := range movies {
		number := apiMovieNumber(movie)
		if number == "" || !strings.EqualFold(number, keyword) || !hashIDRe.MatchString(movie.ID) {
			continue
		}
		if _, exists := seen[number]; exists {
			continue
		}
		seen[number] = struct{}{}

		title := apiMovieTitle(movie, number)
		if title == "" {
			title = number
		}
		cover := m.absoluteURL(firstNonEmpty(
			movie.WideThumbnail,
			movie.CoverImage,
			movie.ThumbnailImage,
		))
		result := &model.MovieSearchResult{
			ID:          number,
			Number:      number,
			Title:       title,
			Provider:    Name,
			Homepage:    m.detailURL(movie.ID),
			ThumbURL:    cover,
			CoverURL:    cover,
			Score:       apiMovieScore(movie),
			ReleaseDate: parser.ParseDate(movie.ReleaseDate),
		}
		if result.IsValid() {
			results = append(results, result)
		}
	}
	if len(results) == 0 {
		return nil, provider.ErrInfoNotFound
	}
	return results, nil
}

func (m *Manko) getAPIMovieInfo(id, homepage string) (*model.MovieInfo, error) {
	movieID := id
	if !hashIDRe.MatchString(movieID) {
		movie, err := m.findAPIMovie(id)
		if err != nil {
			return nil, err
		}
		movieID = movie.ID
	}
	if !hashIDRe.MatchString(movieID) {
		return nil, provider.ErrInfoNotFound
	}

	data, err := m.requestMankoAPI("/swx/movie/detail/"+url.PathEscape(movieID), nil)
	if err != nil {
		return nil, err
	}
	var movie mankoAPIMovie
	if err := json.Unmarshal(data, &movie); err != nil {
		return nil, fmt.Errorf("%s: decode API detail: %w", Name, err)
	}
	if movie.ID == "" {
		movie.ID = movieID
	}

	number := apiMovieNumber(movie)
	if number == "" {
		number = javnumber.Normalize(id)
	}
	if number == "" {
		number = javnumber.Find(firstNonEmpty(localizedAPITitle(movie), apiMovieSummary(movie)))
	}
	if number == "" {
		return nil, provider.ErrInfoNotFound
	}

	title := apiMovieTitle(movie, number)
	if title == "" {
		title = number
	}
	if homepage == "" {
		homepage = m.detailURL(movieID)
	}

	previews := make([]string, 0, len(movie.BackdropImages)+len(movie.PreviewImageURLS))
	for _, rawURL := range append(append([]string(nil), movie.BackdropImages...), movie.PreviewImageURLS...) {
		previews = appendUnique(previews, m.absoluteURL(rawURL))
	}
	genres := make([]string, 0, len(movie.Tags))
	for _, genre := range movie.Tags {
		genres = appendUnique(genres, genre)
	}

	cover := m.absoluteURL(firstNonEmpty(movie.CoverImage, movie.WideThumbnail, movie.ThumbnailImage))
	thumb := m.absoluteURL(firstNonEmpty(movie.ThumbnailImage, movie.CoverImage, movie.WideThumbnail))
	bigCover := m.absoluteURL(firstNonEmpty(movie.WideThumbnail, movie.CoverImage))
	info := &model.MovieInfo{
		ID:              number,
		Number:          number,
		Title:           title,
		Summary:         apiMovieSummary(movie),
		Provider:        Name,
		Homepage:        homepage,
		Actors:          []string{},
		ThumbURL:        thumb,
		BigThumbURL:     bigCover,
		CoverURL:        cover,
		BigCoverURL:     bigCover,
		PreviewImages:   previews,
		Maker:           firstNonEmpty(movie.Maker, movie.Country),
		Genres:          genres,
		Score:           apiMovieScore(movie),
		Runtime:         movie.VideoDuration,
		ReleaseDate:     parser.ParseDate(movie.ReleaseDate),
		PreviewVideoURL: "",
	}
	if info.ThumbURL == "" {
		info.ThumbURL = info.CoverURL
	}
	if info.CoverURL == "" && len(info.PreviewImages) > 0 {
		info.CoverURL = info.PreviewImages[0]
		info.ThumbURL = info.CoverURL
	}

	if data, actorErr := m.requestMankoAPI("/swx/movie/actors/"+url.PathEscape(movieID),
		url.Values{"size": {"6"}, "page": {"1"}}); actorErr == nil {
		var actors []mankoAPIActor
		if json.Unmarshal(data, &actors) == nil {
			names := make([]string, 0, len(actors))
			for _, actor := range actors {
				name := firstNonEmpty(
					actor.AlsoKnownAs["jp"],
					actor.AlsoKnownAs["ja"],
					actor.Name,
					actor.AlsoKnownAs["en"],
				)
				names = appendUnique(names, name)
			}
			info.Actors = names
		}
	}

	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (m *Manko) findAPIMovie(keyword string) (*mankoAPIMovie, error) {
	keyword = m.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	movies, err := m.fetchAPIMovies(keyword)
	if err != nil {
		return nil, err
	}
	for i := range movies {
		if strings.EqualFold(apiMovieNumber(movies[i]), keyword) && hashIDRe.MatchString(movies[i].ID) {
			return &movies[i], nil
		}
	}
	return nil, provider.ErrInfoNotFound
}

func (m *Manko) fetchAPIMovies(keyword string) ([]mankoAPIMovie, error) {
	data, err := m.requestMankoAPI("/swx/movie/search", url.Values{
		"keyword":   {keyword},
		"size":      {"24"},
		"page":      {"1"},
		"can_manko": {"true"},
	})
	if err != nil {
		return nil, err
	}
	var movies []mankoAPIMovie
	if err := json.Unmarshal(data, &movies); err != nil {
		return nil, fmt.Errorf("%s: decode API search: %w", Name, err)
	}
	return movies, nil
}

func (m *Manko) requestMankoAPI(path string, query url.Values) ([]byte, error) {
	hosts := m.getAPIHosts()
	var failures []string
	for _, host := range hosts {
		endpoint, err := mankoAPIEndpoint(host, path, query)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		resp, err := m.Get(endpoint, fetch.WithHeaders(map[string]string{
			"Accept":          "application/json, text/plain, */*",
			"Accept-Language": "ja-JP,ja;q=0.9",
			"Referer":         m.japaneseHomepage(),
		}))
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, apiResponseLimit+1))
		resp.Body.Close()
		if readErr != nil {
			failures = append(failures, readErr.Error())
			continue
		}
		if len(body) > apiResponseLimit {
			failures = append(failures, "response exceeds size limit")
			continue
		}

		var envelope mankoAPIEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if strings.TrimSpace(envelope.Data) == "" {
			failures = append(failures, "empty API payload")
			continue
		}
		data, err := decodeMankoAPIData(envelope.Data)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		return data, nil
	}
	if len(failures) == 0 {
		failures = append(failures, "no API hosts available")
	}
	return nil, fmt.Errorf("%s: API request failed: %s", Name, strings.Join(failures, "; "))
}

func (m *Manko) getAPIHosts() []string {
	m.apiMu.RLock()
	hosts := append([]string(nil), m.apiHosts...)
	m.apiMu.RUnlock()
	if len(hosts) > 0 {
		return hosts
	}

	var discovered []string
	resp, err := m.Get(m.japaneseHomepage(), fetch.WithHeaders(map[string]string{
		"Accept-Language": "ja-JP,ja;q=0.9",
		"Referer":         m.URL().String(),
	}))
	if err == nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, apiResponseLimit+1))
		resp.Body.Close()
		if readErr == nil {
			for _, match := range apiHostRe.FindAllSubmatch(body, -1) {
				if host := normalizeMankoAPIHost(string(match[1])); host != "" {
					discovered = appendUnique(discovered, host)
				}
			}
		}
	}
	if len(discovered) == 0 {
		discovered = []string{
			"https://healertanker.com",
			"https://alpharomeo.shop",
		}
	}

	m.apiMu.Lock()
	if len(m.apiHosts) == 0 {
		m.apiHosts = append([]string(nil), discovered...)
	} else {
		discovered = append([]string(nil), m.apiHosts...)
	}
	m.apiMu.Unlock()
	return discovered
}

func (m *Manko) japaneseHomepage() string {
	return m.URL().ResolveReference(&url.URL{Path: "/ja/"}).String()
}

func (m *Manko) searchURL(keyword string) string {
	return m.URL().ResolveReference(&url.URL{
		Path: "/ja/searchresult",
		RawQuery: url.Values{
			"by":      {"Title"},
			"keyword": {keyword},
		}.Encode(),
	}).String()
}

func (m *Manko) detailURL(id string) string {
	return m.URL().ResolveReference(&url.URL{
		Path: "/ja/movie-info/" + id,
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
	headers.Set("Accept-Language", "ja-JP,ja;q=0.9")
	headers.Set("Referer", m.japaneseHomepage())
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

		title := firstText(card.Find("div.title span").First())
		if title == "" {
			title = firstAttr(link, "title")
		}
		if title == "" {
			title = id
		}

		cover := ""
		if style, ok := card.Find(`div[style*="background-image"]`).First().Attr("style"); ok {
			if match := backgroundImageRe.FindStringSubmatch(style); len(match) == 2 {
				cover = m.absoluteURL(match[1])
			}
		}

		result := &model.MovieSearchResult{
			ID:          id,
			Number:      id,
			Title:       javnumber.CleanTitle(title, id),
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
		case "title", "タイトル":
			if n := normalizeSearchNumber(value); n != "" {
				info.Number = n
			}
		case "summary", "overview", "概要", "あらすじ":
			info.Summary = value
		case "rating", "評価", "评分":
			info.Score = parser.ParseScore(value)
		case "video duration", "再生時間", "収録時間", "時間":
			info.Runtime = parser.ParseRuntime(value)
		case "maker", "メーカー", "メーカー名":
			info.Maker = value
		case "release date", "発売日", "配信日":
			info.ReleaseDate = parser.ParseDate(value)
		case "actor", "actors", "出演者", "出演":
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
	if info.CoverURL == "" && len(info.PreviewImages) > 0 {
		info.CoverURL = info.PreviewImages[0]
		info.ThumbURL = info.CoverURL
	}

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

func decodeMankoAPIData(encoded string) ([]byte, error) {
	var (
		accumulator uint32
		bits        uint
		queue       = -1
		decoded     = make([]byte, 0, len(encoded))
	)
	for i := 0; i < len(encoded); i++ {
		value := strings.IndexByte(apiBase85Chars, encoded[i])
		if value < 0 {
			continue
		}
		if queue < 0 {
			queue = value
			continue
		}

		queue += 91 * value
		accumulator |= uint32(queue) << bits
		if queue&8191 > 88 {
			bits += 13
		} else {
			bits += 14
		}
		for bits > 7 {
			decoded = append(decoded, byte(accumulator))
			accumulator >>= 8
			bits -= 8
		}
		queue = -1
	}
	if queue > -1 {
		decoded = append(decoded, byte(accumulator|uint32(queue)<<bits))
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("%s: empty API payload after decoding", Name)
	}
	return decoded, nil
}

func mankoAPIEndpoint(host, path string, query url.Values) (string, error) {
	base, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse Manko API host: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("invalid Manko API host scheme %q", base.Scheme)
	}
	if base.Hostname() == "" {
		return "", fmt.Errorf("invalid Manko API host %q", host)
	}
	base.Path = path
	base.RawPath = ""
	base.RawQuery = query.Encode()
	base.Fragment = ""
	return base.String(), nil
}

func normalizeMankoAPIHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		if strings.ContainsAny(raw, "/?#") {
			return ""
		}
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func apiMovieNumber(movie mankoAPIMovie) string {
	for _, candidate := range apiMovieTitleCandidates(movie) {
		if number := normalizeSearchNumber(candidate); number != "" {
			return number
		}
	}
	return ""
}

func apiMovieTitleCandidates(movie mankoAPIMovie) []string {
	candidates := make([]string, 0, len(movie.Titles)+1)
	if movie.Title != "" {
		candidates = append(candidates, movie.Title)
	}
	for _, title := range movie.Titles {
		if title.Title != "" {
			candidates = append(candidates, title.Title)
		}
	}
	return candidates
}

func localizedAPITitle(movie mankoAPIMovie) string {
	for _, wanted := range []string{"ja", "en"} {
		for _, title := range movie.Titles {
			if strings.EqualFold(title.Language, wanted) && strings.TrimSpace(title.Title) != "" {
				return cleanText(title.Title)
			}
		}
	}
	for _, title := range movie.Titles {
		if strings.TrimSpace(title.Title) != "" {
			return cleanText(title.Title)
		}
	}
	return cleanText(movie.Title)
}

func localizedAPIDescription(movie mankoAPIMovie) string {
	for _, wanted := range []string{"ja", "en"} {
		for _, description := range movie.Descriptions {
			if strings.EqualFold(description.Language, wanted) &&
				strings.TrimSpace(description.Overview) != "" {
				return cleanText(description.Overview)
			}
		}
	}
	for _, description := range movie.Descriptions {
		if strings.TrimSpace(description.Overview) != "" {
			return cleanText(description.Overview)
		}
	}
	return ""
}

func apiMovieTitle(movie mankoAPIMovie, number string) string {
	title := localizedAPITitle(movie)
	description := localizedAPIDescription(movie)
	if title == "" {
		title = description
	} else if description != "" &&
		!strings.Contains(strings.ToLower(title), strings.ToLower(description)) {
		title = strings.TrimSpace(title + " " + description)
	}
	return javnumber.CleanTitle(title, number)
}

func apiMovieSummary(movie mankoAPIMovie) string {
	return localizedAPIDescription(movie)
}

func apiMovieScore(movie mankoAPIMovie) float64 {
	if score := parser.ParseScore(movie.IMDbScore); score > 0 {
		return score
	}
	return parser.ParseScore(movie.Rating)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
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
