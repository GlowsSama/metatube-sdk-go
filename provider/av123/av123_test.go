package av123

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

func newTestAV123(t *testing.T, handler http.HandlerFunc) *AV123 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	a := New()
	a.Fetcher = fetch.Default(&fetch.Config{Referer: server.URL + "/"})
	a.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/ja/", Priority, language.Japanese,
		scraper.WithHeaders(map[string]string{
			"Referer":         server.URL + "/",
			"Accept-Language": "ja-JP,ja;q=0.9",
		}))
	return a
}

func av123Card(href, title, cover, duration string) string {
	return fmt.Sprintf(`<article class="card">
<a class="card__cover" href="%s"><img class="card__img" src="%s" alt="%s"></a>
<div class="card__poster" data-preview="%s"></div>
<div class="card__meta"><h3 class="card__title"><a class="card__link" href="%s">%s</a></h3>
<span class="card__dur">%s</span></div>
</article>`, href, cover, title, cover, href, title, duration)
}

func av123DetailPage() string {
	return `<!doctype html><html><head>
<meta property="og:image" content="https://123av.com/logo.png">
<meta property="og:description" content="123 AV 最高の日本のJAVサイト。永久無料、高速、ラグなし、25万本以上の動画、毎日更新、動画再生中に広告なし。">
</head><body>
<div class="player" style="background-image: url('https://icdn.123av.me/img2/s500/70/snos-377/cover.jpg?6adb428b');"></div>
<h1 class="watch__title">SNOS-377 — 「今日はアブノーマルなエッチがしたい…」 そう甘えるCA彼女を無茶苦茶にしたらドMに目覚めた 河北彩花 （ブルーレイディスク）</h1>
<dl class="watch__info">
  <div class="watch__info-row"><dt>コード</dt><dd>SNOS-377</dd></div>
  <div class="watch__info-row"><dt>タイプ</dt><dd><a class="chip" href="/ja/censored">検閲済み</a></dd></div>
  <div class="watch__info-row"><dt>発売日</dt><dd>2026-09-17</dd></div>
  <div class="watch__info-row"><dt>出演者</dt><dd><a class="chip" href="/ja/actresses/hebei-ayaka">河北彩伽(河北彩花)</a></dd></div>
  <div class="watch__info-row"><dt>メーカー</dt><dd><a class="chip" href="/ja/makers/s1">ｴｽﾜﾝ ﾅﾝﾊﾞｰﾜﾝｽﾀｲﾙ</a></dd></div>
  <div class="watch__info-row"><dt>ジャンル</dt><dd><a class="chip">単体作品</a><a class="chip">ｲﾗﾏﾁｵ</a><a class="chip">淫乱･ﾊｰﾄﾞ系</a></dd></div>
  <div class="watch__info-row"><dt>タグ</dt><dd><a class="chip">SNOS</a></dd></div>
</dl>
</body></html>`
}

func TestAV123_NormalizeAndParseURL(t *testing.T) {
	a := New()
	for input, want := range map[string]string{
		"snos377":    "SNOS-377",
		"SNOS-00377": "SNOS-00377",
		"IPZ 950":    "IPZ-950",
	} {
		assert.Equal(t, want, a.NormalizeMovieID(input), input)
	}

	for input, want := range map[string]string{
		"https://123av.com/ja/v/snos-377":     "SNOS-377",
		"https://www.123av.com/ja/v/ipzz-950": "IPZZ-950",
	} {
		id, err := a.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://123av.com/ja/v/snos-377-uncensored-leaked",
		"https://123av.com/ja/v/snos-377-english-subtitle",
		"https://123av.com/en/v/snos-377",
		"https://123av.com/ja/search?keyword=SNOS-377",
		"https://example.com/ja/v/snos-377",
		"file:///ja/v/snos-377",
	} {
		_, err := a.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL, input)
	}
}

func TestAV123_SearchFiltersVariantsAndDuplicates(t *testing.T) {
	a := newTestAV123(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search", r.URL.Path)
		assert.Equal(t, "SNOS-377", r.URL.Query().Get("keyword"))
		assert.Equal(t, "ja-JP,ja;q=0.9", r.Header.Get("Accept-Language"))
		fmt.Fprint(w,
			av123Card("/ja/v/snos-377-uncensored-leaked",
				"SNOS-377-Uncensored-Leaked — Leaked title", "/leaked.jpg", "2:41:02")+
				av123Card("/ja/v/snos-377",
					"SNOS-377 — Exact title", "/exact.jpg", "2:41:02")+
				av123Card("/ja/v/snos-377",
					"SNOS-377 — Duplicate title", "/duplicate.jpg", "2:41:02")+
				av123Card("/ja/v/ipzz-950",
					"IPZZ-950 — Related title", "/related.jpg", "2:00:00"))
	})

	results, err := a.SearchMovie("SNOS-377.mkv")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "SNOS-377", results[0].ID)
	assert.Equal(t, "SNOS-377", results[0].Number)
	assert.Equal(t, "Exact title", results[0].Title)
	assert.Contains(t, results[0].Homepage, "/ja/v/snos-377")
}

func TestAV123_SearchFiltersIPZZ(t *testing.T) {
	a := newTestAV123(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, av123Card("/ja/v/ipzz-950",
			"IPZZ-950 — Related title", "/related.jpg", "2:00:00"))
	})

	results, err := a.SearchMovie("IPZ-950")
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestAV123_GetMovieInfoByID(t *testing.T) {
	a := newTestAV123(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search":
			fmt.Fprint(w, av123Card(
				"/ja/v/snos-377",
				"SNOS-377 — Search title",
				"/thumb.jpg",
				"2:41:02",
			))
		case "/ja/v/snos-377":
			fmt.Fprint(w, av123DetailPage())
		default:
			http.NotFound(w, r)
		}
	})

	info, err := a.GetMovieInfoByID("SNOS377")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "SNOS-377", info.ID)
	assert.Equal(t, "SNOS-377", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t,
		"「今日はアブノーマルなエッチがしたい…」 そう甘えるCA彼女を無茶苦茶にしたらドMに目覚めた 河北彩花 (ブルーレイディスク)",
		info.Title)
	assert.Equal(t, "2026-09-17", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, []string{"河北彩伽(河北彩花)"}, []string(info.Actors))
	assert.Equal(t, "エスワン ナンバーワンスタイル", info.Maker)
	assert.Equal(t, []string{"単体作品", "イラマチオ", "淫乱・ハード系", "SNOS"}, []string(info.Genres))
	assert.Equal(t,
		"https://icdn.123av.me/img2/s500/70/snos-377/cover.jpg?6adb428b",
		info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	assert.Empty(t, info.Summary)
	assert.Zero(t, info.Runtime)
	assert.Contains(t, info.Homepage, "/ja/v/snos-377")
}

func TestAV123_SearchNoResults(t *testing.T) {
	a := newTestAV123(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><main><h1>検索結果</h1></main></body></html>`)
	})

	results, err := a.SearchMovie("IPZ-950")
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestAV123_DetectPageError(t *testing.T) {
	validPage := `<html><head><title>SNOS-377 - 123AV</title></head><body>
<div class="player" style="background-image:url(/cover.jpg)"></div>
</body></html>`
	assert.NoError(t, detectPageError([]byte(validPage)))

	for _, challenge := range []string{
		`<html><head><title>Just a moment...</title></head></html>`,
		`<html><body><div id="challenge-form"></div></body></html>`,
		`<html><body><script>window.__cf_chl_opt = {};</script></body></html>`,
		`<html><body><div class="cf-browser-verification"></div></body></html>`,
	} {
		assert.ErrorIs(t, detectPageError([]byte(challenge)), errAntiBotChallenge)
	}
}

func TestAV123_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1", r.URL.Path)
		var payload struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		response := ""
		switch {
		case strings.Contains(payload.URL, "/ja/search"):
			response = av123Card("/ja/v/snos-377",
				"SNOS-377 — Solved search", "/solved-thumb.jpg", "2:41:02")
		case strings.Contains(payload.URL, "/ja/v/snos-377"):
			response = av123DetailPage()
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

	a := New()
	a.Scraper = scraper.NewDefaultScraper(Name, target.URL+"/ja/", Priority, language.Japanese)
	require.NoError(t, a.SetFlareSolverr(flare.URL+"/v1"))

	info, err := a.GetMovieInfoByID("SNOS-377")
	require.NoError(t, err)
	assert.Equal(t,
		"「今日はアブノーマルなエッチがしたい…」 そう甘えるCA彼女を無茶苦茶にしたらドMに目覚めた 河北彩花 (ブルーレイディスク)",
		info.Title)
	assert.Equal(t,
		"https://icdn.123av.me/img2/s500/70/snos-377/cover.jpg?6adb428b",
		info.CoverURL)
}

func TestAV123_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	a := New()
	a.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, a.SetFlareSolverr(endpoint))

	info, err := a.GetMovieInfoByID("SNOS-377")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.IsValid())
	assert.Equal(t, "SNOS-377", info.Number)
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
