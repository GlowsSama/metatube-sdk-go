package javdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/number"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/fc2/fc2util"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/domainpool"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider        = (*JavDB)(nil)
	_ provider.MovieSearcher        = (*JavDB)(nil)
	_ provider.Fetcher              = (*JavDB)(nil)
	_ provider.DomainSetter         = (*JavDB)(nil)
	_ provider.PrimaryDomainSetter  = (*JavDB)(nil)
	_ provider.DomainConfigurer     = (*JavDB)(nil)
	_ provider.URLMatcher           = (*JavDB)(nil)
	_ provider.SessionConfigurer    = (*JavDB)(nil)
	_ provider.SessionAPIConfigurer = (*JavDB)(nil)

	scorePattern        = regexp.MustCompile(`\d+(?:\.\d+)?`)
	idPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	upperNumberPattern  = regexp.MustCompile(`^[A-Z]{1,10}\d{2,}$`)
	numericPattern      = regexp.MustCompile(`^\d+$`)
	fc2AttemptPattern   = regexp.MustCompile(`(?i)^FC2(?:[-_\s]?PPV)?[-_\s]?\d`)
	numberPartsPattern  = regexp.MustCompile(`^([A-Z]+)-([0-9]+)([A-Z]?)$`)
	errLoginRequired    = errors.New("JavDB login required; configure a JavDB App API token, cookie, or account")
	errGeoRestricted    = errors.New("JavDB is unavailable in the current network region")
	errAntiBotChallenge = errors.New("JavDB returned an anti-bot challenge")
	errDomainCooldown   = errors.New("JavDB request cooldown is active")
	domainCooldowns     = [...]time.Duration{
		15 * time.Minute,
		30 * time.Minute,
		time.Hour,
		2 * time.Hour,
	}
)

const (
	Name              = "JavDB"
	Priority          = 1000 - 10
	baseURL           = "https://javdb.com/"
	maxDomainAttempts = 3
)

// JavDB provides public search and detail-page metadata. Detail-page IDs are
// JavDB's opaque slugs and must retain their original case. FC2 numbers remain
// numeric for backward compatibility and use the public search-card fallback.
type JavDB struct {
	*fetch.Fetcher
	*scraper.Scraper
	domains             *domainpool.Pool
	domainMu            sync.RWMutex
	primaryDomain       *url.URL
	sessionConfigMu     sync.Mutex
	sessionMu           sync.Mutex
	sessionDomains      map[string]struct{}
	sessionAppAPI       *javDBAppAPI
	domainPolicyMu      sync.Mutex
	domainFailureRounds int
	domainCooldownUntil time.Time
	now                 func() time.Time
}

func New() *JavDB {
	return &JavDB{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(Name, baseURL, Priority, language.Chinese,
			scraper.WithHeaders(map[string]string{
				"Referer":         baseURL,
				"Accept-Language": "zh-CN,zh;q=0.9",
			})),
		domains:        domainpool.New(),
		sessionDomains: make(map[string]struct{}),
		now:            time.Now,
	}
}

func (jav *JavDB) URL() *url.URL {
	jav.domainMu.RLock()
	primary := jav.primaryDomain
	jav.domainMu.RUnlock()
	if primary != nil {
		cloned := *primary
		return &cloned
	}
	if jav.Scraper != nil && jav.Scraper.URL() != nil {
		cloned := *jav.Scraper.URL()
		return &cloned
	}
	fallback, _ := url.Parse(baseURL)
	return fallback
}

func (jav *JavDB) SetPrimaryDomain(domain string) error {
	_, _, err := jav.ConfigureDomains(&domain, nil)
	return err
}

func (jav *JavDB) SetBackupDomains(domains []string) error {
	_, _, err := jav.ConfigureDomains(nil, &domains)
	return err
}

func (jav *JavDB) ConfigureDomains(primary *string, backups *[]string) (string, []string, error) {
	jav.sessionConfigMu.Lock()
	defer jav.sessionConfigMu.Unlock()

	var normalizedPrimary *url.URL
	if primary != nil {
		domain := strings.TrimSpace(*primary)
		if domain != "" {
			var err error
			normalizedPrimary, err = domainpool.Normalize(domain)
			if err != nil {
				return "", nil, err
			}
		}
	}

	var normalizedBackups []*url.URL
	if backups != nil {
		var err error
		normalizedBackups, err = domainpool.NormalizeBackups(*backups)
		if err != nil {
			return "", nil, err
		}
	}

	if primary != nil {
		current := jav.URL().String()
		target := current
		if normalizedPrimary != nil {
			target = normalizedPrimary.String()
		} else if jav.Scraper != nil && jav.Scraper.URL() != nil {
			target = jav.Scraper.URL().String()
		}

		jav.sessionMu.Lock()
		if target != current {
			if err := jav.clearSessionStateLocked(); err != nil {
				jav.sessionMu.Unlock()
				return "", nil, err
			}
		}
		jav.domainMu.Lock()
		jav.primaryDomain = normalizedPrimary
		jav.domainMu.Unlock()
		jav.sessionMu.Unlock()
	}

	if backups != nil {
		jav.domains.SetBackupURLs(normalizedBackups)
	}

	jav.domainPolicyMu.Lock()
	jav.clearDomainFailuresLocked()
	jav.domainPolicyMu.Unlock()

	return jav.URL().String(), jav.Domains(), nil
}

func (jav *JavDB) Domains() []string {
	return jav.domains.Domains(jav.URL())
}

func runDomainAttempts[T any](
	operation func(*url.URL) (T, error),
	base *url.URL,
) (T, error, bool) {
	var lastErr error
	for attempt := 0; attempt < maxDomainAttempts; attempt++ {
		value, err := operation(base)
		if err == nil {
			return value, nil, false
		}
		if errors.Is(err, errLoginRequired) {
			var zero T
			return zero, err, true
		}
		if stopDomainFallback(err) {
			var zero T
			return zero, err, false
		}
		lastErr = err
	}
	var zero T
	return zero, lastErr, false
}

// ConfigureSession applies an App API token and optional website fallback
// credentials. Authorization is only used by the separate mobile API client.
// JavDB may require an image captcha for website account login.
func (jav *JavDB) ConfigureSession(
	username, password, cookie, authorization string,
) (provider.SessionStatus, error) {
	return jav.ConfigureSessionWithAPI(username, password, cookie, authorization,
		defaultAppAPIBaseURL)
}

// ConfigureSessionWithAPI applies a JavDB App API token and optional website
// fallback credentials. The token is never attached to website requests.
func (jav *JavDB) ConfigureSessionWithAPI(
	username, password, cookie, authorization, apiURL string,
) (provider.SessionStatus, error) {
	username = strings.TrimSpace(username)
	cookie = strings.TrimSpace(cookie)
	token, err := normalizeAuthorization(authorization)
	if err != nil {
		return provider.SessionStatus{}, err
	}
	appAPI, err := newJavDBAppAPI(token, apiURL, nil)
	if err != nil {
		return provider.SessionStatus{}, err
	}

	jav.sessionConfigMu.Lock()
	defer jav.sessionConfigMu.Unlock()

	jav.sessionMu.Lock()

	if username == "" && password == "" && cookie == "" && token == "" {
		if err := jav.clearSessionStateLocked(); err != nil {
			jav.sessionMu.Unlock()
			return provider.SessionStatus{}, err
		}
		jav.sessionMu.Unlock()
		return provider.SessionStatus{
			Configured: false,
			Mode:       "none",
			Message:    "JavDB 登录信息已清除",
		}, nil
	}

	if err := jav.clearSessionStateLocked(); err != nil {
		jav.sessionMu.Unlock()
		return provider.SessionStatus{}, err
	}

	jav.sessionAppAPI = appAPI

	if cookie != "" {
		cookies, err := parseCookieHeader(cookie)
		if err != nil {
			jav.sessionMu.Unlock()
			return provider.SessionStatus{}, err
		}
		if err := jav.applySessionCookies(cookies); err != nil {
			jav.sessionMu.Unlock()
			return provider.SessionStatus{}, err
		}
		jav.sessionMu.Unlock()
		mode := "cookie"
		message := "JavDB Cookie 已应用；如果已过期，请重新从浏览器获取"
		if appAPI != nil {
			mode = "app_api+cookie"
			message = "JavDB App API Token 和网页 Cookie 已应用；App API 优先兜底"
		}
		return provider.SessionStatus{
			Configured:    true,
			Authenticated: true,
			Mode:          mode,
			Message:       message,
		}, nil
	}

	if appAPI != nil && (username == "" || password == "") {
		jav.sessionMu.Unlock()
		return provider.SessionStatus{
			Configured:    true,
			Authenticated: true,
			Mode:          "app_api",
			Message:       "JavDB App API Token 已应用；仅用于备用站要求登录后的兜底",
		}, nil
	}

	if username == "" || password == "" {
		jav.sessionMu.Unlock()
		return provider.SessionStatus{
			Configured: false,
			Mode:       "password",
			Message:    "JavDB 账号和密码需要同时填写",
		}, nil
	}
	jav.sessionMu.Unlock()

	status, err := jav.loginWithPassword(username, password)
	if err == nil && status.Authenticated {
		jav.sessionMu.Lock()
		jav.rememberSessionDomainLocked(jav.URL().String())
		if appAPI != nil {
			status.Mode = "app_api+password"
			status.Message = "JavDB App API Token 和账号密码已应用；App API 优先兜底"
		}
		jav.sessionMu.Unlock()
	}
	return status, err
}

func (jav *JavDB) MatchesURL(u *url.URL) bool {
	return u != nil && (jav.domains.Matches(jav.URL(), u) || isSiteHost(u.Hostname()))
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
	if (u.Scheme != "http" && u.Scheme != "https") || !jav.MatchesURL(u) {
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
		if id == "" || !isCatalogKeyword(id) {
			return "", provider.ErrInvalidURL
		}
		return id, nil
	default:
		return "", provider.ErrInvalidURL
	}
}

func (jav *JavDB) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := jav.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return jav.GetMovieInfoByID(id)
}

func (jav *JavDB) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = jav.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}

	return executeWithDomainPolicy(jav, func(base *url.URL) (*model.MovieInfo, error) {
		if isNumberLike(id) {
			return jav.getMovieInfoByNumber(base, id)
		}
		return jav.getMovieInfoByDetailURL(base, jav.detailURL(base, id), id)
	}, func() (*model.MovieInfo, error) {
		api := jav.appAPIClient()
		if api == nil {
			return nil, errAppAPIUnavailable
		}
		if isNumberLike(id) {
			return api.movieInfoByNumber(context.Background(), id)
		}
		return api.movieInfo(context.Background(), id)
	})
}

func (jav *JavDB) NormalizeMovieKeyword(keyword string) string {
	keyword = cleanText(keyword)
	if keyword == "" {
		return ""
	}
	if fc2ID := fc2util.ParseNumber(keyword); fc2ID != "" {
		return "FC2-" + fc2ID
	}
	if strings.IndexFunc(keyword, unicode.IsSpace) >= 0 {
		return keyword
	}
	keyword = strings.TrimSpace(number.Trim(keyword))
	if keyword == "" {
		return ""
	}
	keyword = strings.ToUpper(keyword)
	if numericPattern.MatchString(keyword) {
		return "FC2-" + keyword
	}
	if !isCatalogKeyword(keyword) {
		return ""
	}
	return keyword
}

func (jav *JavDB) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	keyword = jav.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}

	return executeWithDomainPolicy(jav, func(base *url.URL) ([]*model.MovieSearchResult, error) {
		results, err := jav.searchMovie(base, keyword)
		if err != nil {
			return nil, err
		}
		return jav.filterSearchResults(results, keyword), nil
	}, func() ([]*model.MovieSearchResult, error) {
		api := jav.appAPIClient()
		if api == nil {
			return nil, errAppAPIUnavailable
		}
		results, err := api.search(context.Background(), keyword)
		if err != nil {
			return nil, err
		}
		return jav.filterSearchResults(results, keyword), nil
	})
}

func executeWithDomainPolicy[T any](
	jav *JavDB,
	operation func(base *url.URL) (T, error),
	appFallback ...func() (T, error),
) (T, error) {
	jav.domainPolicyMu.Lock()
	defer jav.domainPolicyMu.Unlock()

	now := time.Now
	if jav.now != nil {
		now = jav.now
	}
	currentTime := now()
	if jav.domainCooldownUntil.After(currentTime) {
		var zero T
		return zero, fmt.Errorf("%w until %s", errDomainCooldown,
			jav.domainCooldownUntil.Format(time.RFC3339))
	}

	var lastErr error
	candidates := jav.domains.Candidates(jav.URL())
	if len(candidates) <= 1 {
		value, err, loginRequired := runDomainAttempts(operation, jav.URL())
		if err == nil {
			jav.clearDomainFailuresLocked()
			return value, nil
		}
		if loginRequired {
			var appErr error
			if len(appFallback) > 0 && appFallback[0] != nil {
				value, err := appFallback[0]()
				if err == nil {
					jav.clearDomainFailuresLocked()
					return value, nil
				}
				if !errors.Is(err, errAppAPIUnavailable) {
					appErr = err
				}
			}
			jav.clearDomainFailuresLocked()
			if appErr != nil {
				var zero T
				return zero, fmt.Errorf("app API fallback failed: %v; website login required: %w",
					appErr, err)
			}
			var zero T
			return zero, err
		}
		if stopDomainFallback(err) {
			jav.clearDomainFailuresLocked()
			return value, err
		}
		lastErr = err
	} else {
		primary := candidates[0]
		loginRequired := false
		for _, base := range candidates[1:] {
			value, err, requiresLogin := runDomainAttempts(operation, base)
			if err == nil {
				jav.clearDomainFailuresLocked()
				return value, nil
			}
			if stopDomainFallback(err) {
				jav.clearDomainFailuresLocked()
				return value, err
			}
			lastErr = err
			if requiresLogin {
				loginRequired = true
				break
			}
		}

		// The authenticated main site is only a fallback when an anonymous
		// backup explicitly requires a login. Ordinary failures never reach it.
		if loginRequired {
			var appErr error
			if len(appFallback) > 0 && appFallback[0] != nil {
				value, err := appFallback[0]()
				if err == nil {
					jav.clearDomainFailuresLocked()
					return value, nil
				}
				if !errors.Is(err, errAppAPIUnavailable) {
					appErr = err
				}
			}

			value, err, requiresLogin := runDomainAttempts(operation, primary)
			if err == nil {
				jav.clearDomainFailuresLocked()
				return value, nil
			}
			if requiresLogin || stopDomainFallback(err) {
				jav.clearDomainFailuresLocked()
				return value, err
			}
			if appErr != nil {
				err = fmt.Errorf("app API fallback failed: %v; website fallback failed: %w", appErr, err)
			}
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = errors.New("JavDB: no domains available")
	}
	jav.startDomainCooldownLocked(currentTime)

	var zero T
	return zero, fmt.Errorf("%w until %s (last error: %w)", errDomainCooldown,
		jav.domainCooldownUntil.Format(time.RFC3339), lastErr)
}

func (jav *JavDB) clearDomainFailuresLocked() {
	jav.domainFailureRounds = 0
	jav.domainCooldownUntil = time.Time{}
}

func (jav *JavDB) startDomainCooldownLocked(now time.Time) {
	index := jav.domainFailureRounds
	if index >= len(domainCooldowns) {
		index = len(domainCooldowns) - 1
	}
	jav.domainCooldownUntil = now.Add(domainCooldowns[index])
	if jav.domainFailureRounds < len(domainCooldowns) {
		jav.domainFailureRounds++
	}
}

func (jav *JavDB) searchMovie(base *url.URL, keyword string) ([]*model.MovieSearchResult, error) {
	body, err := jav.fetchPage(jav.searchURL(base, keyword))
	if err != nil {
		return nil, err
	}
	return jav.parseSearchResults(base, body)
}

func (jav *JavDB) filterSearchResults(results []*model.MovieSearchResult, keyword string) []*model.MovieSearchResult {
	if !isCatalogKeyword(keyword) {
		type scoredResult struct {
			result *model.MovieSearchResult
			score  float64
		}

		scored := make([]scoredResult, 0, len(results))
		for _, result := range results {
			if result == nil {
				continue
			}
			score := titleSearchScore(keyword, result.Title)
			if score < 0.35 {
				continue
			}
			scored = append(scored, scoredResult{result: result, score: score})
		}
		sort.SliceStable(scored, func(i, j int) bool {
			if scored[i].score == scored[j].score {
				return scored[i].result.ID < scored[j].result.ID
			}
			return scored[i].score > scored[j].score
		})
		if len(scored) > 8 {
			scored = scored[:8]
		}
		filtered := make([]*model.MovieSearchResult, 0, len(scored))
		for _, item := range scored {
			filtered = append(filtered, item.result)
		}
		return filtered
	}

	filtered := make([]*model.MovieSearchResult, 0, len(results))
	for _, result := range results {
		if result == nil || !equivalentMovieNumbers(jav.NormalizeMovieKeyword(result.Number), keyword) {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func (jav *JavDB) getMovieInfoByNumber(base *url.URL, n string) (*model.MovieInfo, error) {
	keyword := jav.NormalizeMovieKeyword(n)
	if keyword == "" {
		return nil, provider.ErrInvalidID
	}
	results, err := jav.searchMovie(base, keyword)
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
	return jav.getMovieInfoByDetailURL(base, result.Homepage, result.ID)
}

func (jav *JavDB) getMovieInfoByDetailURL(base *url.URL, rawURL, id string) (*model.MovieInfo, error) {
	body, err := jav.fetchPage(rawURL)
	if err != nil {
		return nil, err
	}
	return jav.parseMovieInfo(base, body, id, rawURL)
}

func (jav *JavDB) fetchPage(rawURL string) ([]byte, error) {
	c := jav.ClonedCollector()

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

func normalizeAuthorization(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "authorization:") {
		value = strings.TrimSpace(value[len("authorization:"):])
		lower = strings.ToLower(value)
	}
	if strings.HasPrefix(lower, "bearer ") {
		value = strings.TrimSpace(value[len("bearer "):])
	}
	if value == "" {
		return "", errors.New("JavDB Authorization is empty")
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("JavDB Authorization contains invalid line breaks")
	}
	return value, nil
}

func (jav *JavDB) clearSessionStateLocked() error {
	if err := jav.clearSessionCookies(); err != nil {
		return err
	}
	jav.sessionAppAPI = nil
	return nil
}

func (jav *JavDB) appAPIClient() *javDBAppAPI {
	jav.sessionMu.Lock()
	defer jav.sessionMu.Unlock()
	return jav.sessionAppAPI
}

func (jav *JavDB) clearSessionCookies() error {
	domainSet := make(map[string]struct{}, len(jav.sessionDomains)+1)
	for domain := range jav.sessionDomains {
		domainSet[domain] = struct{}{}
	}
	for _, candidate := range jav.domains.Candidates(jav.URL()) {
		domainSet[candidate.String()] = struct{}{}
	}
	domainSet[jav.URL().String()] = struct{}{}

	domains := make([]string, 0, len(domainSet))
	for domain := range domainSet {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	for _, domain := range domains {
		if err := jav.ResetCookies(domain); err != nil {
			return fmt.Errorf("%s: clear cookies for %s: %w", jav.Name(), domain, err)
		}
	}
	jav.sessionDomains = make(map[string]struct{})
	return nil
}

func (jav *JavDB) applySessionCookies(cookies []*http.Cookie) error {
	hostCookies := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		item := *cookie
		item.Domain = ""
		if item.Path == "" {
			item.Path = "/"
		}
		hostCookies = append(hostCookies, &item)
	}
	domain := jav.URL().String()
	if err := jav.SetCookies(domain, hostCookies); err != nil {
		return fmt.Errorf("%s: set cookies for %s: %w", jav.Name(), domain, err)
	}
	jav.rememberSessionDomainLocked(domain)
	return nil
}

func (jav *JavDB) rememberSessionDomainLocked(domain string) {
	if domain == "" {
		return
	}
	if jav.sessionDomains == nil {
		jav.sessionDomains = make(map[string]struct{})
	}
	jav.sessionDomains[domain] = struct{}{}
}

func (jav *JavDB) loginWithPassword(username, password string) (provider.SessionStatus, error) {
	base := jav.URL()
	signinURL := base.ResolveReference(&url.URL{Path: "/login"}).String()
	body, err := jav.fetchPage(signinURL)
	if err != nil {
		return provider.SessionStatus{}, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return provider.SessionStatus{}, fmt.Errorf("%s: parse sign-in page: %w", jav.Name(), err)
	}
	if !isLoginDocument(doc) {
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    "JavDB 未显示登录表单，可能当前会话已经登录",
		}, nil
	}
	if hasCaptcha(doc) {
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    "JavDB 要求图片验证码，账号密码无法自动完成登录；请从已登录浏览器填写 Cookie",
		}, nil
	}

	csrf := strings.TrimSpace(doc.Find(`meta[name="csrf-token"]`).First().AttrOr("content", ""))
	if csrf == "" {
		csrf = strings.TrimSpace(doc.Find(
			`input[name="_csrf"], input[name="authenticity_token"], input[name="csrf-token"]`,
		).First().AttrOr("value", ""))
	}
	if csrf == "" {
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    "JavDB 登录页未提供 CSRF 令牌，无法提交账号密码",
		}, nil
	}

	action := strings.TrimSpace(doc.Find(
		`form[action*="/auth/signin"], form[action*="/auth/login"], form[action*="/login"], form[action*="sign_in"]`,
	).First().AttrOr("action", ""))
	if action == "" {
		action = "/auth/signin"
	}
	actionURL, err := url.Parse(action)
	if err != nil {
		return provider.SessionStatus{}, fmt.Errorf("%s: parse sign-in form action: %w", jav.Name(), err)
	}
	actionURL = base.ResolveReference(actionURL)

	body, status, err := jav.postSignin(actionURL.String(), signinURL, csrf, username, password)
	if err != nil {
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    fmt.Sprintf("JavDB 登录请求失败：%v", err),
		}, nil
	}
	if status >= http.StatusBadRequest {
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    fmt.Sprintf("JavDB 登录请求返回 HTTP %d", status),
		}, nil
	}

	doc, err = goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return provider.SessionStatus{}, fmt.Errorf("%s: parse sign-in response: %w", jav.Name(), err)
	}
	if isLoginDocument(doc) {
		message := "JavDB 账号或密码未被接受"
		if hasCaptcha(doc) {
			message = "JavDB 要求图片验证码，账号密码无法自动完成登录；请从已登录浏览器填写 Cookie"
		}
		return provider.SessionStatus{
			Configured: true,
			Mode:       "password",
			Message:    message,
		}, nil
	}
	return provider.SessionStatus{
		Configured:    true,
		Authenticated: true,
		Mode:          "password",
		Message:       "JavDB 账号密码登录成功",
	}, nil
}

func (jav *JavDB) postSignin(actionURL, refererURL, csrf, username, password string) ([]byte, int, error) {
	c := jav.ClonedCollector()
	var (
		body           []byte
		responseStatus int
		responseErr    error
	)
	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("X-CSRF-Token", csrf)
		r.Headers.Set("Referer", refererURL)
		if u, err := url.Parse(refererURL); err == nil && u.Host != "" {
			r.Headers.Set("Origin", (&url.URL{Scheme: u.Scheme, Host: u.Host}).String())
		}
	})
	c.OnResponse(func(r *colly.Response) {
		body = append([]byte(nil), r.Body...)
		responseStatus = r.StatusCode
	})
	c.OnError(func(r *colly.Response, err error) {
		if r != nil {
			if len(r.Body) > 0 {
				body = append([]byte(nil), r.Body...)
			}
			responseStatus = r.StatusCode
		}
		responseErr = err
	})

	if err := c.Post(actionURL, map[string]string{
		"page":       "1",
		"email":      username,
		"password":   password,
		"_rucaptcha": "",
		"remember":   "1",
	}); err != nil && responseErr == nil {
		responseErr = err
	}
	return body, responseStatus, responseErr
}

func (jav *JavDB) parseSearchResults(base *url.URL, body []byte) ([]*model.MovieSearchResult, error) {
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

		cover := jav.absoluteURL(base, firstAttr(item.Find(".cover img").First(),
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
			Homepage:    jav.absoluteURL(base, href),
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
		return []*model.MovieSearchResult{}, nil
	}
	return nil, fmt.Errorf("%s: unrecognized search response", jav.Name())
}

func (jav *JavDB) parseMovieInfo(base *url.URL, body []byte, id, homepage string) (*model.MovieInfo, error) {
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
	info.CoverURL = jav.absoluteURL(base, firstAttr(coverSelection,
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
		preview := jav.absoluteURL(base, firstAttr(image, "href", "data-src", "src"))
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

func (jav *JavDB) detailURL(base *url.URL, id string) string {
	return base.ResolveReference(&url.URL{Path: "/v/" + id}).String()
}

func (jav *JavDB) searchURL(base *url.URL, keyword string) string {
	query := url.Values{
		"q":      {keyword},
		"f":      {"all"},
		"locale": {"zh"},
	}
	return base.ResolveReference(&url.URL{Path: "/search", RawQuery: query.Encode()}).String()
}

func (jav *JavDB) absoluteURL(base *url.URL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

func isNumberLike(id string) bool {
	return numericPattern.MatchString(id) ||
		strings.ContainsAny(id, "-_") ||
		upperNumberPattern.MatchString(id)
}

func isCatalogKeyword(keyword string) bool {
	keyword = strings.ToUpper(strings.TrimSpace(keyword))
	if keyword == "" || strings.Contains(keyword, " ") {
		return false
	}
	if fc2util.ParseNumber(keyword) != "" || numericPattern.MatchString(keyword) {
		return true
	}
	return isNumberLike(keyword)
}

func equivalentMovieNumbers(left, right string) bool {
	left = normalizeNumberKey(left)
	right = normalizeNumberKey(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}

	leftParts := numberPartsPattern.FindStringSubmatch(left)
	rightParts := numberPartsPattern.FindStringSubmatch(right)
	if len(leftParts) == 4 && len(rightParts) == 4 {
		return leftParts[1] == rightParts[1] &&
			trimLeadingZeros(leftParts[2]) == trimLeadingZeros(rightParts[2]) &&
			leftParts[3] == rightParts[3]
	}

	return false
}

func normalizeNumberKey(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-_")
}

func trimLeadingZeros(value string) string {
	trimmed := strings.TrimLeft(value, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

func titleSearchScore(query, title string) float64 {
	queryKey := normalizeTitleKey(query)
	titleKey := normalizeTitleKey(title)
	if queryKey == "" || titleKey == "" {
		return 0
	}
	if queryKey == titleKey {
		return 1
	}
	if len([]rune(queryKey)) >= 6 &&
		(strings.Contains(titleKey, queryKey) || strings.Contains(queryKey, titleKey)) {
		return 0.92
	}

	queryGrams := titleBigrams(queryKey)
	titleGrams := titleBigrams(titleKey)
	if len(queryGrams) == 0 || len(titleGrams) == 0 {
		return 0
	}
	common := 0
	for gram := range queryGrams {
		if _, ok := titleGrams[gram]; ok {
			common++
		}
	}
	return 2 * float64(common) / float64(len(queryGrams)+len(titleGrams))
}

func normalizeTitleKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func titleBigrams(value string) map[string]struct{} {
	runes := []rune(value)
	if len(runes) < 2 {
		return map[string]struct{}{}
	}
	grams := make(map[string]struct{}, len(runes)-1)
	for index := 0; index < len(runes)-1; index++ {
		grams[string(runes[index:index+2])] = struct{}{}
	}
	return grams
}

func isSiteHost(host string) bool {
	host = strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(host), "."), "www.")
	return host == "javdb.com"
}

func stopDomainFallback(err error) bool {
	// A normal lookup miss is authoritative for the current query. Login pages,
	// HTTP errors, anti-bot challenges and geo restrictions are domain-specific,
	// so those errors continue through the bounded backup-domain pool.
	return errors.Is(err, provider.ErrInfoNotFound) ||
		errors.Is(err, provider.ErrInvalidID) ||
		errors.Is(err, provider.ErrInvalidKeyword)
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
	case strings.Contains(lower, "<title>just a moment...</title>"),
		strings.Contains(lower, "<title>attention required! | cloudflare</title>"),
		strings.Contains(lower, "cf-browser-verification"),
		strings.Contains(lower, "__cf_chl_"),
		strings.Contains(lower, `id="challenge-form"`),
		strings.Contains(lower, `id='challenge-form'`):
		return errAntiBotChallenge
	default:
		return nil
	}
}

func isLoginDocument(doc *goquery.Document) bool {
	if doc.Find(`input[type="password"], input[name="password"]`).Length() == 0 {
		return false
	}
	title := strings.ToLower(cleanText(doc.Find("title").First().Text()))
	return doc.Find(`form[action*="/auth/signin"], form[action*="/auth/login"], form[action*="/login"], form[action*="sign_in"]`).Length() > 0 ||
		strings.Contains(title, "登入") ||
		strings.Contains(title, "登录") ||
		strings.Contains(title, "login") ||
		strings.Contains(title, "sign in")
}

func hasCaptcha(doc *goquery.Document) bool {
	return doc.Find(`img[src*="rucaptcha"], input[name="_rucaptcha"], script[src*="rucaptcha"]`).Length() > 0
}

func parseCookieHeader(raw string) ([]*http.Cookie, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("Cookie:") && strings.EqualFold(raw[:len("Cookie:")], "Cookie:") {
		raw = strings.TrimSpace(raw[len("Cookie:"):])
	}
	if raw == "" {
		return nil, fmt.Errorf("empty JavDB cookie")
	}
	req := &http.Request{Header: http.Header{"Cookie": []string{raw}}}
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("JavDB cookie is not a valid Cookie header")
	}
	return cookies, nil
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
