package avfan

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

func newTestAVFan(t *testing.T, handler http.HandlerFunc) *AVFan {
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

func avfanCard(slug, number, title, cover, date, rating string) string {
	return fmt.Sprintf(`<div class="flex flex-col relative group">
<a href="/ja/movies/%s"><img src="%s" alt="%s"></a>
<a href="/ja/movies/%s" title="[%s] %s">
  <div class="title"><strong class="history-link">%s</strong><span>%s</span></div>
  <span title="%s点 · 評価100件">%s点 · 評価100件</span>
  <div class="text-xs text-zinc-600">%s</div>
</a>
</div>`, slug, cover, title, slug, number, title, number, title, rating, rating, date)
}

func avfanDetailPage() string {
	return `<!doctype html><html><head>
<meta property="og:image" content="https://c1.avfan.com/covers/wd/wdtjYJ.jpg">
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"VideoObject","name":"SNOS-347 新人お天気キャスターと始発電車で遭遇！","description":"一行目<br><br>二行目","thumbnailUrl":"https://c1.avfan.com/covers/wd/wdtjYJ.jpg","url":"https://avfan.com/ja/movies/zuO6xU4x","contentUrl":"https://example.com/preview.mp4","uploadDate":"2026-10-09T10:58:44+08:00","duration":"PT120M","aggregateRating":{"@type":"AggregateRating","ratingValue":4.3,"bestRating":5,"worstRating":1,"ratingCount":732}}
</script>
</head><body>
<img data-movie-target="cover" src="https://c1.avfan.com/covers/wd/wdtjYJ.jpg">
<h1 data-movie-target="title">新人お天気キャスターと始発電車で遭遇！</h1>
<dl class="movie-info">
  <dt>Number</dt><dd><span class="movie-number"><a>SNOS</a>-347</span><button data-number="SNOS-347"></button></dd>
  <dt>Released Date</dt><dd>10/09/2026</dd>
  <dt>Duration</dt><dd>120 mins</dd>
  <dt>Maker</dt><dd><a href="/ja/makers/example">エスワン ナンバーワンスタイル</a></dd>
  <dt>Publisher</dt><dd><a href="/ja/publishers/example">S1 NO.1 STYLE</a></dd>
  <dt>Director</dt><dd><a href="/ja/directors/example">昇天シロー</a></dd>
  <dt>Cast</dt><dd><a href="/ja/casts/one">鈴木希</a>, <a href="/ja/casts/two">イセドン内村</a></dd>
  <dt>Tags</dt><dd><a class="tag-chip">4K</a><a class="tag-chip">単体作品</a></dd>
</dl>
<a data-fancybox="gallery" href="https://c1.avfan.com/samples/8h/8H450R/l_1.jpg"></a>
<a data-fancybox="gallery" href="https://c1.avfan.com/samples/8h/8H450R/l_2.jpg"></a>
</body></html>`
}

func TestAVFan_NormalizeAndParseURL(t *testing.T) {
	a := New()
	for input, want := range map[string]string{
		"snos347":    "SNOS-347",
		"SNOS 347":   "SNOS-347",
		"SNOS-00347": "SNOS-00347",
		"zuO6xU4x":   "zuO6xU4x",
	} {
		assert.Equal(t, want, a.NormalizeMovieID(input), input)
	}

	for input, want := range map[string]string{
		"https://avfan.com/ja/movies/zuO6xU4x":     "zuO6xU4x",
		"https://www.avfan.com/en/movies/m7EUp19Z": "m7EUp19Z",
	} {
		id, err := a.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://avfan.com/ja/search?q=SNOS-347&st=movie",
		"https://example.com/ja/movies/zuO6xU4x",
		"file:///ja/movies/zuO6xU4x",
	} {
		_, err := a.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL, input)
	}
}

func TestAVFan_SearchFiltersVariantsAndDuplicates(t *testing.T) {
	a := newTestAVFan(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search", r.URL.Path)
		assert.Equal(t, "SNOS-347", r.URL.Query().Get("q"))
		assert.Equal(t, "movie", r.URL.Query().Get("st"))
		fmt.Fprint(w,
			avfanCard("related01", "IPZZ-950", "IPZZ-950 unrelated",
				"/related.jpg", "2020-01-01", "3.0")+
				avfanCard("zuO6xU4x", "SNOS-347",
					"新人お天気キャスターと始発電車で遭遇！",
					"https://c1.avfan.com/covers/wd/wdtjYJ.jpg", "2026-10-09", "4.33")+
				avfanCard("zuO6xU4x", "SNOS-347", "Duplicate",
					"/duplicate.jpg", "2026-10-09", "4.33"))
	})

	results, err := a.SearchMovie("SNOS-00347")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "zuO6xU4x", results[0].ID)
	assert.Equal(t, "SNOS-347", results[0].Number)
	assert.Equal(t, "新人お天気キャスターと始発電車で遭遇！", results[0].Title)
	assert.Equal(t, "https://c1.avfan.com/covers/wd/wdtjYJ.jpg", results[0].CoverURL)
	assert.Equal(t, "2026-10-09", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
	assert.InDelta(t, 4.33, results[0].Score, 0.001)
}

func TestAVFan_GetMovieInfoBySlug(t *testing.T) {
	a := newTestAVFan(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ja/movies/zuO6xU4x", r.URL.Path)
		fmt.Fprint(w, avfanDetailPage())
	})

	info, err := a.GetMovieInfoByID("zuO6xU4x")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "zuO6xU4x", info.ID)
	assert.Equal(t, "SNOS-347", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, "新人お天気キャスターと始発電車で遭遇！", info.Title)
	assert.Equal(t, "一行目 二行目", info.Summary)
	assert.Equal(t, "2026-10-09", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 120, info.Runtime)
	assert.InDelta(t, 4.3, info.Score, 0.001)
	assert.Equal(t, "エスワン ナンバーワンスタイル", info.Maker)
	assert.Equal(t, "S1 NO.1 STYLE", info.Label)
	assert.Equal(t, "昇天シロー", info.Director)
	assert.Equal(t, []string{"鈴木希", "イセドン内村"}, []string(info.Actors))
	assert.Equal(t, []string{"4K", "単体作品"}, []string(info.Genres))
	assert.Equal(t, "https://c1.avfan.com/covers/wd/wdtjYJ.jpg", info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	assert.Equal(t, "https://example.com/preview.mp4", info.PreviewVideoURL)
	assert.Len(t, info.PreviewImages, 2)
}

func TestAVFan_GetMovieInfoByNumber(t *testing.T) {
	a := newTestAVFan(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search":
			fmt.Fprint(w, avfanCard("zuO6xU4x", "SNOS-347",
				"新人お天気キャスターと始発電車で遭遇！",
				"/thumb.jpg", "2026-10-09", "4.33"))
		case "/ja/movies/zuO6xU4x":
			fmt.Fprint(w, avfanDetailPage())
		default:
			http.NotFound(w, r)
		}
	})

	info, err := a.GetMovieInfoByID("SNOS-00347")
	require.NoError(t, err)
	assert.Equal(t, "SNOS-347", info.Number)
	assert.Equal(t, "zuO6xU4x", info.ID)
}

func TestAVFan_SearchNoResults(t *testing.T) {
	a := newTestAVFan(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><main><h1>検索結果</h1></main></body></html>`)
	})

	results, err := a.SearchMovie("IPZ-950")
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestAVFan_CleanSearchTitle(t *testing.T) {
	assert.Equal(t, "タイトル", cleanSearchTitle("[SNOS-347] SNOS-347 タイトル", "SNOS-347"))
	assert.Equal(t, "SNOS-347", cleanSearchTitle("", "SNOS-347"))
	assert.Equal(t, "タイトル", cleanSearchTitle(strings.Repeat(" ", 3)+"タイトル", "SNOS-347"))
}
