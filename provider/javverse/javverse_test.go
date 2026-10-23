package javverse

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

type detailFact struct {
	label string
	value string
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) *JavVerse {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	jv := New()
	jv.Fetcher = fetch.Default(&fetch.Config{Referer: server.URL + "/"})
	jv.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/", Priority, language.Japanese,
		scraper.WithHeaders(map[string]string{
			"Referer":         server.URL + "/",
			"Accept-Language": "ja-JP,ja;q=0.9",
		}))
	return jv
}

func searchCard(workID, number, title, cover, date, rating string) string {
	return fmt.Sprintf(`<a class="work-card" href="/ja/work/%s">
<div class="work-cover"><img src="%s" alt="%sのジャケット"></div>
<div class="work-card-meta">
  <div class="work-card-code"><span class="code-plate code-plate-small" aria-label="%s"></span>
    <span class="rating-display" aria-label="%s"></span>
  </div>
  <h3 lang="ja">%s</h3>
  <div class="work-card-bottom"><span>%s</span></div>
</div>
</a>`, workID, cover, number, number, rating, title, date)
}

func detailPage(number, title, cover, rating string, facts []detailFact, stills []string) string {
	var factsHTML strings.Builder
	for _, fact := range facts {
		fmt.Fprintf(&factsHTML, "<div><dt>%s</dt><dd>%s</dd></div>", fact.label, fact.value)
	}

	var stillsHTML strings.Builder
	for _, still := range stills {
		fmt.Fprintf(&stillsHTML, `<a class="still" href="%s"><img src="%s"></a>`, still, still)
	}

	return fmt.Sprintf(`<!doctype html><html><head>
<meta property="og:image" content="%s">
</head><body>
<div class="detail-cover"><img src="%s" alt="%sのジャケット"></div>
<h1 lang="ja">%s</h1>
<span class="rating-display" aria-label="%s"></span>
<div class="detail-facts"><dl>%s</dl></div>
<section class="work-stills">%s</section>
</body></html>`, cover, cover, number, title, rating, factsHTML.String(), stillsHTML.String())
}

func ipz950DetailPage(title string) string {
	return detailPage(
		"IPZ-950",
		title,
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950pl.jpg",
		"評価なし",
		[]detailFact{
			{label: "発売日", value: "2017-05-20"},
			{label: "出演者", value: "桃乃木かな"},
			{label: "収録時間", value: "118分"},
			{label: "監督", value: "はんきゅう太郎"},
			{label: "制作メーカー", value: "アイデアポケット"},
			{label: "レーベル", value: "ティッシュ"},
			{label: "シリーズ", value: "夫の目の前で犯●れて"},
			{label: "ジャンル", value: "人妻・主婦\nドラマ\n単体作品\nデジモ\nハイビジョン\n独占配信"},
			{label: "情報元の番号", value: "IPZ-00950"},
		},
		[]string{
			"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950jp-1.jpg",
			"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950jp-2.jpg",
		},
	)
}

func snos377DetailPage() string {
	return detailPage(
		"SNOS-377",
		"「今日はアブノーマルなエッチがしたい…」 そう甘えるCA彼女を無茶苦茶にしたらドMに目覚めた 河北彩花",
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/snos00377/snos00377pl.jpg",
		"9.8 / 10",
		[]detailFact{
			{label: "発売日", value: "2026-09-18"},
			{label: "出演者", value: "河北彩花（河北彩伽）"},
			{label: "収録時間", value: "162分"},
			{label: "監督", value: "イナバール"},
			{label: "制作メーカー", value: "エスワン ナンバーワンスタイル"},
			{label: "レーベル", value: "S1 NO.1 STYLE"},
			{label: "シリーズ", value: "未登録"},
			{label: "ジャンル", value: "拘束\n淫乱・ハード系\nイラマチオ\nM女\nアクメ・オーガズム\n単体作品\nハイビジョン\n独占配信\n4K"},
			{label: "情報元の番号", value: "SNOS-00377"},
		},
		[]string{
			"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/snos00377/snos00377jp-1.jpg",
		},
	)
}

func TestJavVerse_NormalizeAndParseURL(t *testing.T) {
	jv := New()
	for input, want := range map[string]string{
		"ipz950":         "IPZ-950",
		"IPZ_950":        "IPZ-950",
		"SNOS 377":       "SNOS-377",
		"IPZ-00950":      "IPZ-950",
		"SNOS-00377":     "SNOS-377",
		"326288":         "326288",
		"391742.html":    "391742",
		"unrecognized":   "",
		"12345678901234": "12345678901234",
	} {
		assert.Equal(t, want, jv.NormalizeMovieID(input))
	}

	for input, want := range map[string]string{
		"https://javverse.com/ja/work/326288":             "326288",
		"https://javverse.com/en/work/391742/":            "391742",
		"https://javverse.com/ja/search?q=IPZ-950":        "IPZ-950",
		"https://www.javverse.com/zh/search?q=SNOS-00377": "SNOS-377",
	} {
		id, err := jv.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://example.com/ja/work/326288",
		"https://javverse.com/ja/work/not-a-number",
		"https://javverse.com/ja/search",
		"file:///ja/work/326288",
	} {
		_, err := jv.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestJavVerse_SearchMovieFiltersAndDeduplicates(t *testing.T) {
	jv := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search", r.URL.Path)
		assert.Equal(t, "IPZ-950", r.URL.Query().Get("q"))
		assert.Equal(t, "ja-JP,ja;q=0.9", r.Header.Get("Accept-Language"))
		fmt.Fprint(w,
			searchCard("326288", "IPZ-950", "IPZ-950 IPZ-950 アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな",
				"https://awsimgsrc.dmm.co.jp/ipz00950pl.jpg", "2017-05-20", "評価なし")+
				searchCard("999999", "IPZZ-950", "IPZZ-950 unrelated",
					"/related.jpg", "2019-01-01", "8.0 / 10")+
				searchCard("326288", "IPZ-950", "Duplicate",
					"/duplicate.jpg", "2017-05-20", "評価なし"))
	})

	results, err := jv.SearchMovie("ipz950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "326288", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", results[0].Title)
	assert.Equal(t, "https://awsimgsrc.dmm.co.jp/ipz00950pl.jpg", results[0].CoverURL)
	assert.Contains(t, results[0].Homepage, "/ja/work/326288")
	assert.Equal(t, "2017-05-20", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
}

func TestJavVerse_GetMovieInfoByID(t *testing.T) {
	jv := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search":
			fmt.Fprint(w, searchCard("326288", "IPZ-950", "Search result",
				"/search.jpg", "2017-05-20", "評価なし"))
		case "/ja/work/326288":
			fmt.Fprint(w, ipz950DetailPage(
				"IPZ-950 IPZ-950 アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな"))
		default:
			http.NotFound(w, r)
		}
	})

	info, err := jv.GetMovieInfoByID("ipz950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "326288", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", info.Title)
	assert.Equal(t, "2017-05-20", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 118, info.Runtime)
	assert.Equal(t, "はんきゅう太郎", info.Director)
	assert.Equal(t, "アイデアポケット", info.Maker)
	assert.Equal(t, "ティッシュ", info.Label)
	assert.Equal(t, "夫の目の前で犯●れて", info.Series)
	assert.Equal(t, []string{"桃乃木かな"}, []string(info.Actors))
	assert.Equal(t, []string{"人妻・主婦", "ドラマ", "単体作品", "デジモ", "ハイビジョン", "独占配信"}, []string(info.Genres))
	assert.Equal(t,
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950pl.jpg",
		info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	require.Len(t, info.PreviewImages, 2)
	assert.Equal(t,
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950jp-2.jpg",
		info.PreviewImages[1])
	assert.Contains(t, info.Homepage, "/ja/work/326288")
}

func TestJavVerse_GetMovieInfoByNumericID(t *testing.T) {
	jv := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ja/work/391742", r.URL.Path)
		fmt.Fprint(w, snos377DetailPage())
	})

	info, err := jv.GetMovieInfoByID("391742")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "391742", info.ID)
	assert.Equal(t, "SNOS-377", info.Number)
	assert.Equal(t, "2026-09-18", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 162, info.Runtime)
	assert.Equal(t, "イナバール", info.Director)
	assert.Equal(t, "エスワン ナンバーワンスタイル", info.Maker)
	assert.Equal(t, "S1 NO.1 STYLE", info.Label)
	assert.Empty(t, info.Series)
	assert.Equal(t, []string{"河北彩花（河北彩伽）"}, []string(info.Actors))
	assert.Equal(t, []string{"拘束", "淫乱・ハード系", "イラマチオ", "M女", "アクメ・オーガズム", "単体作品", "ハイビジョン", "独占配信", "4K"}, []string(info.Genres))
	assert.Equal(t, 9.8, info.Score)
	assert.Equal(t,
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/snos00377/snos00377pl.jpg",
		info.CoverURL)
}

func TestJavVerse_SearchNoResults(t *testing.T) {
	jv := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><section class="search-group-empty">
<h2>関連作品は見つかりませんでした</h2>
</section></body></html>`)
	})

	results, err := jv.SearchMovie("IPZ-9500")
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestJavVerse_CleanMovieTitle(t *testing.T) {
	for _, tc := range []struct {
		title  string
		number string
		want   string
	}{
		{
			title:  "IPZ-950 IPZ-950 アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな",
			number: "IPZ-950",
			want:   "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな",
		},
		{
			title:  "SNOS-377 タイトル",
			number: "SNOS-377",
			want:   "タイトル",
		},
		{
			title:  "IPZ-9500 unrelated",
			number: "IPZ-950",
			want:   "IPZ-9500 unrelated",
		},
	} {
		assert.Equal(t, tc.want, cleanMovieTitle(tc.title, tc.number))
	}
}

func TestJavVerse_DetectPageError(t *testing.T) {
	validPage := `<html><head><title>IPZ-950 - JAV Verse</title></head><body>
<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>
<div class="detail-cover">valid content</div>
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

	assert.ErrorIs(t,
		detectPageError([]byte(`<html><head><title>年齢確認</title></head><body>18+</body></html>`)),
		errAgeGateRequired)
}

func TestJavVerse_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
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
			response = searchCard("326288", "IPZ-950", "Solved search",
				"/solved-search.jpg", "2017-05-20", "評価なし")
		case strings.Contains(payload.URL, "/ja/work/326288"):
			response = ipz950DetailPage("Solved detail")
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

	jv := New()
	jv.Scraper = scraper.NewDefaultScraper(Name, target.URL+"/", Priority, language.Japanese)
	require.NoError(t, jv.SetFlareSolverr(flare.URL+"/v1"))

	info, err := jv.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "Solved detail", info.Title)
	assert.Equal(t,
		"https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ipz00950/ipz00950pl.jpg",
		info.CoverURL)
}

func TestJavVerse_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	jv := New()
	jv.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, jv.SetFlareSolverr(endpoint))

	for _, number := range []string{"IPZ-950", "SNOS-377"} {
		t.Run(number, func(t *testing.T) {
			info, err := jv.GetMovieInfoByID(number)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.True(t, info.IsValid())
			assert.Equal(t, number, info.Number)
			assert.NotEmpty(t, info.Title)
			assert.NotEmpty(t, info.CoverURL)
			assert.NotEmpty(t, info.ReleaseDate)
			t.Logf("title=%q release=%s runtime=%d actors=%v cover=%s previews=%d",
				info.Title,
				time.Time(info.ReleaseDate).Format("2006-01-02"),
				info.Runtime,
				info.Actors,
				info.CoverURL,
				len(info.PreviewImages),
			)
		})
	}
}
