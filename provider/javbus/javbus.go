package javbus

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"

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
	_ provider.MovieProvider = (*JavBus)(nil)
	_ provider.MovieSearcher = (*JavBus)(nil)
	_ provider.Fetcher       = (*JavBus)(nil)
)

const (
	Name     = "JavBus"
	Priority = 1000 - 5
)

const (
	baseURL             = "https://www.javbus.com/"
	movieURL            = "https://www.javbus.com/ja/%s"
	searchURL           = "https://www.javbus.com/ja/search/%s"
	searchUncensoredURL = "https://www.javbus.com/ja/uncensored/search/%s"
)

var (
	errVerificationRequired = errors.New("JavBus age verification required")
	labelNumberRe           = regexp.MustCompile(`(?i)^([a-z]{2,})(\d+)$`)
	specialMovieRe          = regexp.MustCompile(`^(?i)([\d-_]{4,}|[a-z]{1,4}\d{2,4}|heyzo[-_].+)$`)
)

type JavBus struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavBus {
	return &JavBus{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(
			Name, baseURL, Priority,
			language.Japanese,
			scraper.WithDisableRedirects(),
			scraper.WithHeaders(map[string]string{
				"Referer": baseURL,
			}),
			scraper.WithCookies(baseURL, []*http.Cookie{
				// existmag=all
				{Name: "dv", Value: "1"},
				{Name: "existmag", Value: "all"},
				// age verification
				{Name: "age", Value: "verified"},
			})),
	}
}

func (bus *JavBus) NormalizeMovieID(id string) string {
	return strings.ToUpper(labelNumberRe.ReplaceAllString(id, "${1}-${2}"))
}

func (bus *JavBus) GetMovieInfoByID(id string) (info *model.MovieInfo, err error) {
	return bus.GetMovieInfoByURL(fmt.Sprintf(movieURL, id))
}

func (bus *JavBus) ParseMovieIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return bus.NormalizeMovieID(path.Base(homepage.Path)), nil
}

func (bus *JavBus) GetMovieInfoByURL(rawURL string) (info *model.MovieInfo, err error) {
	id, err := bus.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return
	}

	info = &model.MovieInfo{
		ID:            id,
		Provider:      bus.Name(),
		Homepage:      rawURL,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	c := bus.ClonedCollector()

	c.OnResponse(func(r *colly.Response) {
		if isVerificationResponse(r) {
			err = errVerificationRequired
		}
	})

	// Image+Title
	c.OnXML(`//a[@class="bigImage"]/img`, func(e *colly.XMLElement) {
		info.Title = e.Attr("title")
		info.CoverURL = e.Request.AbsoluteURL(e.Attr("src"))
	})

	// Fields
	c.OnXML(`//div[@class="col-md-3 info"]/p`, func(e *colly.XMLElement) {
		switch e.ChildText(`.//span`) {
		case "品番:", "ID:":
			info.Number = e.ChildText(`.//span[2]`)
		case "発売日:", "Release Date:":
			fields := strings.Fields(e.Text)
			info.ReleaseDate = parser.ParseDate(fields[len(fields)-1])
		case "収録時間:", "Length:":
			fields := strings.Fields(e.Text)
			info.Runtime = parser.ParseRuntime(fields[len(fields)-1])
		case "監督:", "Director:":
			info.Director = e.ChildText(`.//a`)
		case "メーカー:", "Studio:":
			info.Maker = e.ChildText(`.//a`)
		case "レーベル:", "Label:":
			info.Label = e.ChildText(`.//a`)
		case "シリーズ:", "Series:":
			info.Series = e.ChildText(`.//a`)
		}
	})

	// Genres
	c.OnXML(`//span[@class="genre"]`, func(e *colly.XMLElement) {
		if tag := strings.TrimSpace(e.ChildText(`.//label/a`)); tag != "" {
			info.Genres = append(info.Genres, tag)
		}
	})

	// Previews
	c.OnXML(`//*[@id="sample-waterfall"]/a`, func(e *colly.XMLElement) {
		info.PreviewImages = append(info.PreviewImages, e.Request.AbsoluteURL(e.Attr("href")))
	})

	// Actors
	c.OnXML(`//div[@class="star-name"]`, func(e *colly.XMLElement) {
		info.Actors = append(info.Actors, e.ChildAttr(`.//a`, "title"))
	})

	// Thumb
	c.OnScraped(func(_ *colly.Response) {
		if re := regexp.MustCompile(`(?i)/cover/([a-z\d]+)(?:_b)?\.(jpg|png)`); re.MatchString(info.CoverURL) {
			var (
				thumb  = re.ReplaceAllString(info.CoverURL, "/thumb/${1}.${2}")
				thumbs = re.ReplaceAllString(info.CoverURL, "/thumbs/${1}.${2}")
			)
			var mu sync.Mutex
			d := c.Clone()
			d.Async = true
			d.ParseHTTPErrorResponse = false
			d.OnScraped(func(r *colly.Response) {
				mu.Lock()
				defer mu.Unlock()
				info.ThumbURL = r.Request.URL.String()
			})
			// Head request to test if is a valid thumb url.
			d.Head(thumb)
			d.Head(thumbs)
			d.Wait()
		}
	})

	if visitErr := c.Visit(info.Homepage); visitErr != nil {
		return nil, visitErr
	}
	if errors.Is(err, errVerificationRequired) {
		return nil, err
	}
	return
}

func (bus *JavBus) NormalizeMovieKeyword(keyword string) string {
	keyword = strings.ToUpper(keyword)
	if number.IsSpecial(keyword) && !specialMovieRe.MatchString(keyword) {
		return "" // JavBus has no those special contents.
	}
	return bus.NormalizeMovieID(keyword)
}

func (bus *JavBus) SearchMovie(keyword string) (results []*model.MovieSearchResult, err error) {
	c := bus.ClonedCollector()
	c.Async = true /* ASYNC */

	var mu sync.Mutex
	var verificationRequired bool
	c.OnResponse(func(r *colly.Response) {
		if isVerificationResponse(r) {
			mu.Lock()
			verificationRequired = true
			mu.Unlock()
		}
	})
	c.OnXML(`//a[@class="movie-box"]`, func(e *colly.XMLElement) {
		mu.Lock()
		defer mu.Unlock()

		var thumb, cover string
		thumb = e.Request.AbsoluteURL(e.ChildAttr(`.//div[1]/img`, "src"))
		if re := regexp.MustCompile(`(?i)/thumbs?/([a-z\d]+)(?:_b)?\.(jpg|png)`); re.MatchString(thumb) {
			cover = re.ReplaceAllString(thumb, "/cover/${1}_b.${2}") // guess
		}

		homepage := e.Request.AbsoluteURL(e.Attr("href"))
		id, _ := bus.ParseMovieIDFromURL(homepage)
		results = append(results, &model.MovieSearchResult{
			ID:          id,
			Number:      e.ChildText(`.//div[2]/span/date[1]`),
			Title:       strings.SplitN(e.ChildText(`.//div[2]/span`), "\n", 2)[0],
			Provider:    bus.Name(),
			Homepage:    homepage,
			ThumbURL:    thumb,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(e.ChildText(`.//div[2]/span/date[2]`)),
		})
	})

	for _, u := range []string{
		fmt.Sprintf(searchURL, keyword),
		fmt.Sprintf(searchUncensoredURL, keyword),
	} {
		if err = c.Visit(u); err != nil {
			return nil, err
		}
	}
	c.Wait()

	if verificationRequired {
		return nil, errVerificationRequired
	}

	results = bus.filterSearchResults(results, keyword)

	// JavBus search sometimes omits titles whose detail pages still exist.
	if len(results) == 0 {
		if id := bus.NormalizeMovieKeyword(keyword); id != "" {
			if info, infoErr := bus.GetMovieInfoByID(id); errors.Is(infoErr, errVerificationRequired) {
				return nil, infoErr
			} else if infoErr == nil && info.IsValid() {
				results = append(results, info.ToSearchResult())
			}
		}
	}
	return bus.filterSearchResults(results, keyword), nil
}

func (bus *JavBus) filterSearchResults(results []*model.MovieSearchResult, keyword string) []*model.MovieSearchResult {
	keyword = bus.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil
	}

	filtered := make([]*model.MovieSearchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result == nil || bus.NormalizeMovieKeyword(result.Number) != keyword {
			continue
		}

		key := bus.NormalizeMovieID(result.ID)
		if key == "" {
			key = bus.NormalizeMovieKeyword(result.Number)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, result)
	}
	return filtered
}

func isVerificationResponse(r *colly.Response) bool {
	return r.StatusCode >= http.StatusMultipleChoices &&
		r.StatusCode < http.StatusBadRequest &&
		strings.Contains(r.Headers.Get("Location"), "/doc/driver-verify") ||
		bytes.Contains(r.Body, []byte(`id="ageVerify"`))
}

func init() {
	provider.Register(Name, New)
}
