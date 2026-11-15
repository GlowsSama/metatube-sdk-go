package manko

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

const (
	testHash  = "606ec52122071d3cc15d326e"
	testCover = "https://icdn.123av.me/img2/s500/b8/ipz-950/cover.jpg?6a3478d3"
)

func newTestManko(t *testing.T, handler http.HandlerFunc) *Manko {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	m := New()
	m.Fetcher = fetch.Default(&fetch.Config{Referer: server.URL + "/"})
	m.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/", Priority, language.Japanese,
		scraper.WithHeaders(map[string]string{
			"Referer":         server.URL + "/",
			"Accept-Language": "ja-JP,ja;q=0.9",
		}))
	m.apiHosts = []string{server.URL}
	return m
}

func mankoCard(number, href, cover, date, score string) string {
	return mankoCardWithTitle(number, number, href, cover, date, score)
}

func mankoCardWithTitle(number, title, href, cover, date, score string) string {
	return fmt.Sprintf(`<div class="border border-gray-dark rounded-lg">
<a class="relative block" href="%s"><div style="background-image: url(&quot;%s&quot;); background-size: cover;"></div></a>
<div class="p-3">
  <div class="flex justify-between"><a title="%s" href="%s"><div class="title"><strong>%s</strong><span>%s</span></div></a></div>
  <div><span class="text-gray-300">%s</span><button aria-label="Copy IMDb ID, rating: %s">%s</button></div>
</div>
</div>`, href, cover, number, href, number, title, date, score, score)
}

func mankoDetailPage() string {
	return fmt.Sprintf(`<!doctype html><html><head><title>Search Results | Manko</title></head><body>
<iframe src="https://sadie-shop.site/e/P8J1N6Z8?poster=https%%3A%%2F%%2Ficdn.123av.me%%2Fimg2%%2Fs500%%2Fb8%%2Fipz-950%%2Fcover.jpg%%3F6a3478d3"></iframe>
<video src="https://example.com/ads-cache/video.mp4"></video>
<h1>Attacker's Full Supervision Being Fucked In Front Of Her Husband - Kana Momonogi</h1>
<div class="space-y-2 text-sm">
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Title:</span><span class="text-white">IPZ-950</span></div>
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Rating:</span><span class="text-yellow-400 font-bold">4.55</span></div>
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Video Duration:</span><span class="text-white">120 minutes</span></div>
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Maker:</span><span class="text-white">IDEA POCKET</span></div>
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Size:</span><span class="text-white">5.07 GiB</span></div>
  <div class="flex items-center gap-2"><span class="text-gray-400 font-medium">Release date:</span><span class="text-white">2017-05-24</span></div>
  <div class="flex gap-2"><span class="text-gray-400 font-medium">Actor:</span><span>
    <a href="/actor/0dKX?name=Kana%%20Momonogi">Kana Momonogi, </a>
    <a href="/actor/x7wn?name=Masahiro%%20Tabuchi">Masahiro Tabuchi</a>
  </span></div>
  <h3>Genre:</h3>
  <div>
    <a href="/filter/genre/Solowork">Solowork</a>
    <a href="/filter/genre/Married%%20Woman">Married Woman</a>
    <a href="/filter/genre/Training">Training</a>
    <a href="/filter/genre/Rape">Rape</a>
    <a href="/filter/genre/Drama">Drama</a>
    <a href="/filter/genre/Digital%%20Mosaic">Digital Mosaic</a>
  </div>
</div>
<img alt="Backdrop 1" src="https://image.healertanker.com/%s/samples/xv/xve0n_l_0.jpg">
<img alt="Backdrop 2" src="https://image.healertanker.com/%s/samples/xv/xve0n_l_1.jpg">
</body></html>`, testHash, testHash)
}

func mankoJapaneseDetailPage() string {
	return fmt.Sprintf(`<!doctype html><html lang="ja"><head><title>作品情報 | Manko</title></head><body>
<iframe src="https://sadie-shop.site/e/P8J1N6Z8?poster=https%%3A%%2F%%2Ficdn.123av.me%%2Fimg2%%2Fs500%%2Fb8%%2Fipz-950%%2Fcover.jpg%%3F6a3478d3"></iframe>
<h1>アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな</h1>
<div class="space-y-2 text-sm">
  <div><span class="text-gray-400 font-medium">タイトル:</span><span class="text-white">IPZ-950</span></div>
  <div><span class="text-gray-400 font-medium">あらすじ:</span><span class="text-white">アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな</span></div>
  <div><span class="text-gray-400 font-medium">評価:</span><span class="text-yellow-400 font-bold">4.55</span></div>
  <div><span class="text-gray-400 font-medium">再生時間:</span><span class="text-white">120 分</span></div>
  <div><span class="text-gray-400 font-medium">メーカー:</span><span class="text-white">IDEA POCKET</span></div>
  <div><span class="text-gray-400 font-medium">発売日:</span><span class="text-white">2017-05-24</span></div>
  <div><span class="text-gray-400 font-medium">出演者:</span><span>
    <a href="/actor/0dKX?name=桃乃木かな">桃乃木かな, </a>
    <a href="/actor/x7wn?name=田淵正浩">田淵正浩</a>
  </span></div>
  <div>
    <a href="/filter/genre/Solowork">Solowork</a>
    <a href="/filter/genre/Drama">Drama</a>
  </div>
</div>
<img alt="Backdrop 1" src="https://image.healertanker.com/%s/samples/xv/xve0n_l_0.jpg">
</body></html>`, testHash)
}

func TestManko_NormalizeAndParseURL(t *testing.T) {
	m := New()
	for input, want := range map[string]string{
		"ipz950":                  "IPZ-950",
		"IPZ 950":                 "IPZ-950",
		strings.ToUpper(testHash): testHash,
	} {
		assert.Equal(t, want, m.NormalizeMovieID(input), input)
	}

	for input, want := range map[string]string{
		"https://manko.fun/movie-info/" + testHash:                       testHash,
		"https://www.manko.fun/movie-info/" + testHash + "?series=false": testHash,
	} {
		id, err := m.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://manko.fun/searchresult?keyword=IPZ-950",
		"https://manko.fun/movie-info/short-id",
		"https://example.com/movie-info/" + testHash,
		"file:///movie-info/" + testHash,
	} {
		_, err := m.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL, input)
	}
}

func TestManko_DefaultURLsUseJapaneseLocale(t *testing.T) {
	m := New()
	assert.Equal(t,
		"https://manko.fun/ja/searchresult?by=Title&keyword=IPZ-950",
		m.searchURL("IPZ-950"),
	)
	assert.Equal(t,
		"https://manko.fun/ja/movie-info/"+testHash,
		m.detailURL(testHash),
	)
}

func TestManko_SearchFiltersVariantsAndDuplicates(t *testing.T) {
	body := mankoCard("IPZZ-950", "/movie-info/aaaaaaaaaaaaaaaaaaaaaaaa", "/related.webp", "2020-01-01", "3.00") +
		mankoCard("IPZ-950-Uncensored", "/movie-info/bbbbbbbbbbbbbbbbbbbbbbbb", "/uncensored.webp", "2020-01-01", "3.00") +
		mankoCard("IPZ-950-English-Subtitle", "/movie-info/cccccccccccccccccccccccc", "/subtitle.webp", "2020-01-01", "3.00") +
		mankoCardWithTitle("IPZ-950", "IPZ-950 IPZ-950 Attacker's Full Supervision",
			"/movie-info/"+testHash, testCover, "2017-05-24", "4.55") +
		mankoCard("IPZ-950", "/movie-info/"+testHash+"?series=false", "/duplicate.webp", "2017-05-24", "4.55")

	results, err := New().parseSearchResults([]byte(body), "IPZ-950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-950", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Attacker's Full Supervision", results[0].Title)
	assert.InDelta(t, 4.55, results[0].Score, 0.001)
	assert.Equal(t, testCover, results[0].CoverURL)
	assert.Equal(t, testCover, results[0].ThumbURL)
	assert.Equal(t, "2017-05-24", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
	assert.Contains(t, results[0].Homepage, "/movie-info/"+testHash)
}

func TestManko_SearchTitleFallsBackWithoutRepeatingNumber(t *testing.T) {
	results, err := New().parseSearchResults(
		[]byte(mankoCard("IPZ-950", "/movie-info/"+testHash, testCover, "2017-05-24", "4.55")),
		"IPZ-950",
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-950", results[0].Title)
}

func TestManko_ParseMovieInfo(t *testing.T) {
	homepage := "https://manko.fun/movie-info/" + testHash
	info, err := New().parseMovieInfo([]byte(mankoDetailPage()), testHash, homepage)
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "IPZ-950", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, "Attacker's Full Supervision Being Fucked In Front Of Her Husband - Kana Momonogi", info.Title)
	assert.Equal(t, "2017-05-24", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 120, info.Runtime)
	assert.InDelta(t, 4.55, info.Score, 0.001)
	assert.Equal(t, "IDEA POCKET", info.Maker)
	assert.Equal(t, []string{"Kana Momonogi", "Masahiro Tabuchi"}, []string(info.Actors))
	assert.Equal(t, []string{"Solowork", "Married Woman", "Training", "Rape", "Drama", "Digital Mosaic"}, []string(info.Genres))
	assert.Equal(t, testCover, info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	assert.Empty(t, info.PreviewVideoURL)
	assert.Equal(t, []string{
		"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_0.jpg",
		"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_1.jpg",
	}, []string(info.PreviewImages))
	assert.Equal(t, homepage, info.Homepage)
}

func TestManko_ParseJapaneseMovieInfo(t *testing.T) {
	homepage := "https://manko.fun/movie-info/" + testHash
	info, err := New().parseMovieInfo([]byte(mankoJapaneseDetailPage()), testHash, homepage)
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", info.Title)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", info.Summary)
	assert.Equal(t, "IDEA POCKET", info.Maker)
	assert.Equal(t, []string{"桃乃木かな", "田淵正浩"}, []string(info.Actors))
	assert.Equal(t, 120, info.Runtime)
	assert.InDelta(t, 4.55, info.Score, 0.001)
}

func TestManko_DecodeAPIData(t *testing.T) {
	payload := []byte(`{"title":"IPZ-950","language":"ja","overview":"日本語の概要"}`)
	decoded, err := decodeMankoAPIData(encodeMankoAPIData(payload))
	require.NoError(t, err)
	assert.JSONEq(t, string(payload), string(decoded))
}

func TestManko_APIPrefersJapaneseMetadata(t *testing.T) {
	searchPayload, err := json.Marshal([]mankoAPIMovie{{
		ID:             testHash,
		Title:          "IPZ-950",
		Titles:         []mankoAPILocalizedTitle{{Language: "en", Title: "IPZ-950"}},
		ReleaseDate:    "2017-05-24",
		Rating:         "4.55",
		IMDbScore:      "4.55",
		ThumbnailImage: "https://image.healertanker.com/" + testHash + "/thumbs/xv/xve0n.jpg",
		WideThumbnail:  "https://image.healertanker.com/" + testHash + "/cover.webp",
	}})
	require.NoError(t, err)

	detailPayload, err := json.Marshal(mankoAPIMovie{
		ID:     testHash,
		Titles: []mankoAPILocalizedTitle{{Language: "en", Title: "IPZ-950"}, {Language: "ja", Title: "IPZ-950"}},
		Descriptions: []mankoAPILocalizedText{
			{Language: "en", Overview: "Attacker's Full Supervision Being Fucked In Front Of Her Husband - Kana Momonogi"},
			{Language: "ja", Overview: "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな"},
		},
		Tags:           []string{"Solowork", "Married Woman", "Training", "Rape", "Drama", "Digital Mosaic"},
		Maker:          "IDEA POCKET",
		Country:        "IDEA POCKET",
		ReleaseDate:    "2017-05-24",
		Rating:         "NA",
		IMDbScore:      "4.55",
		VideoDuration:  120,
		ThumbnailImage: "https://image.healertanker.com/" + testHash + "/thumbs/xv/xve0n.jpg",
		WideThumbnail:  "https://image.healertanker.com/" + testHash + "/cover.webp",
		CoverImage:     "https://image.healertanker.com/" + testHash + "/cover.webp",
		BackdropImages: []string{
			"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_0.jpg",
			"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_1.jpg",
		},
	})
	require.NoError(t, err)

	actorPayload, err := json.Marshal([]mankoAPIActor{
		{
			Name: "桃乃木香奈",
			AlsoKnownAs: map[string]string{
				"jp": "桃乃木かな",
				"en": "Kana Momonogi",
			},
		},
		{
			Name:        "田淵正浩",
			AlsoKnownAs: map[string]string{"jp": "田淵正浩", "en": "Masahiro Tabuchi"},
		},
	})
	require.NoError(t, err)

	m := newTestManko(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "ja-JP,ja;q=0.9", r.Header.Get("Accept-Language"))
		assert.Contains(t, r.Header.Get("Referer"), "/ja/")
		w.Header().Set("Content-Type", "application/json")

		var payload []byte
		switch {
		case r.URL.Path == "/swx/movie/search":
			assert.Equal(t, "IPZ-950", r.URL.Query().Get("keyword"))
			payload = searchPayload
		case r.URL.Path == "/swx/movie/detail/"+testHash:
			payload = detailPayload
		case r.URL.Path == "/swx/movie/actors/"+testHash:
			payload = actorPayload
		default:
			http.NotFound(w, r)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(mankoAPIEnvelope{
			Data: encodeMankoAPIData(payload),
		}))
	})

	results, err := m.SearchMovie("IPZ-950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-950", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Title)
	assert.InDelta(t, 4.55, results[0].Score, 0.001)
	assert.Equal(t, "2017-05-24", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, m.detailURL(testHash), results[0].Homepage)

	info, err := m.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "IPZ-950", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", info.Title)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", info.Summary)
	assert.Equal(t, []string{"桃乃木かな", "田淵正浩"}, []string(info.Actors))
	assert.Equal(t, "IDEA POCKET", info.Maker)
	assert.Equal(t, 120, info.Runtime)
	assert.InDelta(t, 4.55, info.Score, 0.001)
	assert.Equal(t, "2017-05-24", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, []string{"Solowork", "Married Woman", "Training", "Rape", "Drama", "Digital Mosaic"}, []string(info.Genres))
	assert.Equal(t, "https://image.healertanker.com/"+testHash+"/cover.webp", info.CoverURL)
	assert.Equal(t, "https://image.healertanker.com/"+testHash+"/thumbs/xv/xve0n.jpg", info.ThumbURL)
	assert.Equal(t, []string{
		"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_0.jpg",
		"https://image.healertanker.com/" + testHash + "/samples/xv/xve0n_l_1.jpg",
	}, []string(info.PreviewImages))
}

func TestManko_SearchNoResults(t *testing.T) {
	results, err := New().parseSearchResults(
		[]byte(`<html><body><main><h1>Search Results</h1></main></body></html>`),
		"IPZ-950",
	)
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestManko_DetectPageError(t *testing.T) {
	assert.NoError(t, detectPageError([]byte(`<html><h1>Search Results</h1></html>`)))
	for _, challenge := range []string{
		`<html><head><title>Just a moment...</title></head></html>`,
		`<html><body><div id="challenge-form"></div></body></html>`,
		`<html><body><script>window.__cf_chl_opt = {};</script></body></html>`,
		`<html><body><div class="cf-browser-verification"></div></body></html>`,
	} {
		assert.ErrorIs(t, detectPageError([]byte(challenge)), errAntiBotChallenge)
	}
}

func TestManko_UsesFlareSolverrForSPAShell(t *testing.T) {
	m := newTestManko(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body><div id="app"></div><script src="/dist/build.js?v=1"></script></body></html>`))
	})

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1", r.URL.Path)
		var payload struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "ja-JP,ja;q=0.9", payload.Headers["Accept-Language"])

		response := ""
		switch {
		case strings.Contains(payload.URL, "/searchresult"):
			response = mankoCard("IPZ-950", "https://manko.fun/movie-info/"+testHash, "/render.webp", "2017-05-24", "4.55")
		case strings.Contains(payload.URL, "/movie-info/"+testHash):
			response = mankoDetailPage()
		default:
			t.Errorf("unexpected FlareSolverr URL: %s", payload.URL)
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status":   http.StatusOK,
				"response": response,
			},
		}))
	}))
	t.Cleanup(flare.Close)

	require.NoError(t, m.SetFlareSolverr(flare.URL+"/v1"))

	results, err := m.SearchMovie("IPZ-950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-950", results[0].ID)
	assert.Equal(t, "https://manko.fun/movie-info/"+testHash, results[0].Homepage)

	info, err := m.GetMovieInfoByID(testHash)
	require.NoError(t, err)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, testCover, info.CoverURL)
}

func TestManko_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/swx/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1", r.URL.Path)
		var payload struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "ja-JP,ja;q=0.9", payload.Headers["Accept-Language"])

		response := ""
		switch {
		case strings.Contains(payload.URL, "/searchresult"):
			response = mankoCard("IPZ-950", "/movie-info/"+testHash, "/solved.webp", "2017-05-24", "4.55")
		case strings.Contains(payload.URL, "/movie-info/"+testHash):
			response = mankoDetailPage()
		default:
			t.Errorf("unexpected FlareSolverr URL: %s", payload.URL)
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status":   http.StatusOK,
				"response": response,
			},
		}))
	}))
	t.Cleanup(flare.Close)

	m := New()
	m.Scraper = scraper.NewDefaultScraper(Name, target.URL+"/", Priority, language.Japanese,
		scraper.WithHeaders(map[string]string{
			"Referer":         target.URL + "/",
			"Accept-Language": "ja-JP,ja;q=0.9",
		}))
	m.apiHosts = []string{target.URL}
	require.NoError(t, m.SetFlareSolverr(flare.URL+"/v1"))

	info, err := m.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, testCover, info.CoverURL)
}

func encodeMankoAPIData(data []byte) string {
	var (
		out       strings.Builder
		bitBuffer int
		bitCount  int
	)
	out.Grow(len(data) * 5 / 4)
	for _, value := range data {
		bitBuffer |= int(value) << bitCount
		bitCount += 8
		if bitCount <= 13 {
			continue
		}

		encoded := bitBuffer & 8191
		if encoded > 88 {
			bitBuffer >>= 13
			bitCount -= 13
		} else {
			encoded = bitBuffer & 16383
			bitBuffer >>= 14
			bitCount -= 14
		}
		out.WriteByte(apiBase85Chars[encoded%91])
		out.WriteByte(apiBase85Chars[encoded/91])
	}
	if bitCount > 0 {
		out.WriteByte(apiBase85Chars[bitBuffer%91])
		if bitCount > 7 || bitBuffer > 90 {
			out.WriteByte(apiBase85Chars[bitBuffer/91])
		}
	}
	return out.String()
}

func TestManko_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	m := New()
	m.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, m.SetFlareSolverr(endpoint))

	info, err := m.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.IsValid())
	assert.Equal(t, "IPZ-950", info.Number)
	assert.NotEmpty(t, info.Title)
	assert.NotEmpty(t, info.CoverURL)
	assert.NotEmpty(t, info.ReleaseDate)
	t.Logf("title=%q release=%s actors=%v maker=%q cover=%s",
		info.Title,
		time.Time(info.ReleaseDate).Format("2006-01-02"),
		info.Actors,
		info.Maker,
		info.CoverURL,
	)
}
