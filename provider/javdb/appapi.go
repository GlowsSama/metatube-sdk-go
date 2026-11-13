package javdb

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/fc2/fc2util"
)

const (
	defaultAppAPIBaseURL = "https://jdforrepam.com/api"
	appAPISignatureKey   = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
	maxAppAPIResponse    = 8 << 20
)

var errAppAPIUnavailable = errors.New("JavDB App API is not configured")

type javDBAppAPI struct {
	baseURL    *url.URL
	token      string
	deviceUUID string
	httpClient *http.Client
}

type javDBAPIValue string

func (v *javDBAPIValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*v = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*v = javDBAPIValue(strings.TrimSpace(value))
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*v = javDBAPIValue(number.String())
	return nil
}

type javDBAPIMovie struct {
	ID              javDBAPIValue `json:"id"`
	Number          string        `json:"number"`
	Title           string        `json:"title"`
	OriginTitle     string        `json:"origin_title"`
	Summary         string        `json:"summary"`
	DirectorName    string        `json:"director_name"`
	MakerName       string        `json:"maker_name"`
	PublisherName   string        `json:"publisher_name"`
	SeriesName      string        `json:"series_name"`
	ReleaseDate     string        `json:"release_date"`
	Duration        javDBAPIValue `json:"duration"`
	CoverURL        string        `json:"cover_url"`
	ThumbURL        string        `json:"thumb_url"`
	PreviewVideoURL string        `json:"preview_video_url"`
	Score           javDBAPIValue `json:"score"`
	Tags            []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Actors []struct {
		Name   string        `json:"name"`
		Gender javDBAPIValue `json:"gender"`
	} `json:"actors"`
	PreviewImages []struct {
		ThumbURL string `json:"thumb_url"`
		LargeURL string `json:"large_url"`
	} `json:"preview_images"`
}

func (movie *javDBAPIMovie) metadataTitle() string {
	if title := cleanText(movie.OriginTitle); title != "" {
		return title
	}
	return cleanText(movie.Title)
}

func newJavDBAppAPI(token, rawBaseURL string, client *http.Client) (*javDBAppAPI, error) {
	if token == "" {
		return nil, nil
	}
	baseURL, err := normalizeAppAPIBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = appAPIRedirectPolicy(baseURL, client.CheckRedirect)
	return &javDBAppAPI{
		baseURL:    baseURL,
		token:      token,
		deviceUUID: newAppAPIDeviceUUID(),
		httpClient: &clientCopy,
	}, nil
}

func appAPIRedirectPolicy(
	baseURL *url.URL,
	next func(*http.Request, []*http.Request) error,
) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil || baseURL == nil ||
			!strings.EqualFold(request.URL.Scheme, baseURL.Scheme) ||
			!strings.EqualFold(request.URL.Host, baseURL.Host) {
			return http.ErrUseLastResponse
		}
		if next != nil {
			return next(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

func normalizeAppAPIBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultAppAPIBaseURL
	}
	baseURL, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse JavDB App API URL: %w", err)
	}
	if (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, errors.New("JavDB App API URL must be an absolute HTTP URL")
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("JavDB App API URL cannot contain credentials, query, or fragment")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	if baseURL.Path == "" {
		baseURL.Path = "/api"
	}
	return baseURL, nil
}

func newAppAPIDeviceUUID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func appAPISignature(timestamp int64) string {
	sum := md5.Sum([]byte(strconv.FormatInt(timestamp, 10) + appAPISignatureKey))
	return fmt.Sprintf("%d.lpw6vgqzsp.%x", timestamp, sum)
}

func (api *javDBAppAPI) endpoint(path string, params url.Values) string {
	query := url.Values{
		"app_channel":        {"official"},
		"app_version":        {"1.9.28"},
		"app_version_number": {"10928"},
		"platform":           {"android"},
		"system_version":     {"13"},
		"device_model":       {"Pixel 6"},
		"device_name":        {"Pixel"},
		"device_uuid":        {api.deviceUUID},
	}
	for key, values := range params {
		query[key] = values
	}
	endpoint := *api.baseURL
	endpoint.Path = strings.TrimRight(api.baseURL.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

func (api *javDBAppAPI) get(ctx context.Context, path string, params url.Values, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, api.endpoint(path, params), nil)
	if err != nil {
		return fmt.Errorf("JavDB App API: build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+api.token)
	request.Header.Set("jdsignature", appAPISignature(time.Now().Unix()))
	request.Header.Set("User-Agent", "Dart/3.4 (dart:io)")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "zh-TW")

	response, err := api.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("JavDB App API: request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("JavDB App API: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAppAPIResponse+1))
	if err != nil {
		return fmt.Errorf("JavDB App API: read response: %w", err)
	}
	if len(body) > maxAppAPIResponse {
		return errors.New("JavDB App API: response too large")
	}

	var envelope struct {
		Success json.RawMessage `json:"success"`
		Action  string          `json:"action"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("JavDB App API: decode response: %w", err)
	}
	success := strings.Trim(string(envelope.Success), `"`)
	if success != "1" && !strings.EqualFold(success, "true") {
		return fmt.Errorf("JavDB App API: request rejected (%s)", cleanText(envelope.Action))
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("JavDB App API: missing response data")
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return fmt.Errorf("JavDB App API: decode data: %w", err)
	}
	return nil
}

func (api *javDBAppAPI) search(ctx context.Context, keyword string) ([]*model.MovieSearchResult, error) {
	keyword = appAPIQueryCode(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}

	var data struct {
		Movies []javDBAPIMovie `json:"movies"`
	}
	err := api.get(ctx, "/v2/search", url.Values{
		"q":     {keyword},
		"page":  {"1"},
		"limit": {"100"},
	}, &data)
	if err != nil {
		return nil, err
	}

	results := make([]*model.MovieSearchResult, 0, len(data.Movies))
	for _, movie := range data.Movies {
		result := appAPISearchResult(movie)
		if result != nil {
			results = append(results, result)
		}
	}
	return results, nil
}

func (api *javDBAppAPI) movieInfo(ctx context.Context, id string) (*model.MovieInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}

	var data json.RawMessage
	if err := api.get(ctx, "/v4/movies/"+url.PathEscape(id), nil, &data); err != nil {
		return nil, err
	}
	var nested struct {
		Movie json.RawMessage `json:"movie"`
	}
	if err := json.Unmarshal(data, &nested); err != nil {
		return nil, fmt.Errorf("JavDB App API: decode detail: %w", err)
	}
	if len(nested.Movie) > 0 && string(nested.Movie) != "null" {
		data = nested.Movie
	}

	var movie javDBAPIMovie
	if err := json.Unmarshal(data, &movie); err != nil {
		return nil, fmt.Errorf("JavDB App API: decode movie: %w", err)
	}
	info := appAPIMovieInfo(movie, api.baseURL)
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}

func (api *javDBAppAPI) movieInfoByNumber(ctx context.Context, number string) (*model.MovieInfo, error) {
	number = appAPIQueryCode(number)
	results, err := api.search(ctx, number)
	if err != nil {
		return nil, err
	}

	var matchedID string
	for _, result := range results {
		if result == nil || !equivalentMovieNumbers(result.Number, number) {
			continue
		}
		if matchedID != "" && matchedID != result.ID {
			return nil, fmt.Errorf("JavDB App API: ambiguous number %s", number)
		}
		matchedID = result.ID
	}
	if matchedID == "" {
		return nil, provider.ErrInfoNotFound
	}
	return api.movieInfo(ctx, matchedID)
}

func appAPIQueryCode(code string) string {
	code = strings.TrimSpace(code)
	if fc2ID := fc2util.ParseNumber(code); fc2ID != "" {
		return "FC2-" + fc2ID
	}
	return code
}

func appAPISearchResult(movie javDBAPIMovie) *model.MovieSearchResult {
	id := strings.TrimSpace(string(movie.ID))
	number := normalizeNumber(movie.Number)
	title := movie.metadataTitle()
	if id == "" || number == "" || title == "" {
		return nil
	}
	if fc2ID := fc2util.ParseNumber(number); fc2ID != "" {
		id = fc2ID
	}

	thumbURL := strings.TrimSpace(movie.ThumbURL)
	if thumbURL == "" {
		thumbURL = strings.TrimSpace(movie.CoverURL)
	}
	result := &model.MovieSearchResult{
		ID:          id,
		Number:      number,
		Title:       title,
		Provider:    Name,
		Homepage:    "https://javdb.com/v/" + id,
		ThumbURL:    thumbURL,
		CoverURL:    strings.TrimSpace(movie.CoverURL),
		Score:       parseScore(string(movie.Score)),
		ReleaseDate: parser.ParseDate(movie.ReleaseDate),
	}
	if !result.IsValid() {
		return nil
	}
	return result
}

func appAPIMovieInfo(movie javDBAPIMovie, apiBaseURL *url.URL) *model.MovieInfo {
	id := strings.TrimSpace(string(movie.ID))
	number := normalizeNumber(movie.Number)
	title := movie.metadataTitle()
	info := &model.MovieInfo{
		ID:            id,
		Number:        number,
		Title:         title,
		Summary:       cleanText(movie.Summary),
		Provider:      Name,
		Homepage:      "https://javdb.com/v/" + id,
		Director:      cleanText(movie.DirectorName),
		Actors:        []string{},
		ThumbURL:      strings.TrimSpace(movie.ThumbURL),
		CoverURL:      strings.TrimSpace(movie.CoverURL),
		Maker:         cleanText(movie.MakerName),
		Label:         cleanText(movie.PublisherName),
		Series:        cleanText(movie.SeriesName),
		Genres:        []string{},
		Score:         parseScore(string(movie.Score)),
		PreviewImages: []string{},
	}
	if info.ThumbURL == "" {
		info.ThumbURL = info.CoverURL
	}
	info.BigThumbURL = info.ThumbURL
	info.BigCoverURL = info.CoverURL
	info.PreviewVideoURL = strings.TrimSpace(movie.PreviewVideoURL)
	info.Runtime, _ = strconv.Atoi(string(movie.Duration))
	if info.Runtime < 0 {
		info.Runtime = 0
	}
	info.ReleaseDate = parser.ParseDate(movie.ReleaseDate)

	for _, tag := range movie.Tags {
		info.Genres = appendUnique(info.Genres, tag.Name)
	}
	for _, actor := range movie.Actors {
		if string(actor.Gender) != "" && string(actor.Gender) != "0" {
			continue
		}
		info.Actors = appendUnique(info.Actors, actor.Name)
	}
	for _, preview := range movie.PreviewImages {
		imageURL := strings.TrimSpace(preview.LargeURL)
		if imageURL == "" {
			imageURL = strings.TrimSpace(preview.ThumbURL)
		}
		if imageURL == "" {
			continue
		}
		if parsed, err := url.Parse(imageURL); err == nil && !parsed.IsAbs() && apiBaseURL != nil {
			imageURL = apiBaseURL.ResolveReference(parsed).String()
		}
		info.PreviewImages = appendUnique(info.PreviewImages, imageURL)
	}
	return info
}
