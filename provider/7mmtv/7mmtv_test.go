package mmtv

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

func newTestProvider(t *testing.T, handler http.HandlerFunc) *MMTV {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	m := New()
	m.Fetcher = fetch.Default(&fetch.Config{Referer: server.URL + "/"})
	m.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/", Priority, language.Japanese)
	return m
}

func searchCard(category, contentID, number, title, cover, date string) string {
	href := fmt.Sprintf("/ja/%s/%s/%s.html", category, contentID, number)
	return fmt.Sprintf(`<div class="col-item"><div class="video">
<figure class="video-preview"><a href="%s"><img data-src="%s"></a></figure>
<h3 class="video-title"><a href="%s">%s</a></h3>
<div class="video-info"><span class="small text-muted">%s</span></div>
</div></div>`, href, cover, href, title, date)
}

func detailPage(number, title, cover string) string {
	return fmt.Sprintf(`<!doctype html><html><body>
<div class="content_main_cover"><img src="%s"></div>
<div class="fullvideo-details">
  <h1 class="fullvideo-title">%s</h1>
  <div class="d-flex mb-4">
    <span class="text-muted me-3">%s</span>
    <span class="text-muted me-3">2017-05-20</span>
    <span class="text-muted me-3">118分（HD版：118分）</span>
  </div>
  <div class="fullvideo-attr row">
    <div class="col-auto"><strong>ジャンル</strong></div>
    <div class="d-flex flex-wrap categories"><a>ドラマ</a><a>人妻</a><a>単体作品</a></div>
  </div>
  <div class="fullvideo-attr row">
    <div class="col-auto"><strong>出演者</strong></div>
    <div class="fullvideo-idol"><span><a>桃乃木かな</a></span></div>
  </div>
  <div class="fullvideo-attr">
    <div class="row"><div><strong>レーベル:</strong></div><div><a>ティッシュ</a></div></div>
    <div class="row"><div><strong>メーカー:</strong></div><div><a>アイデアポケット</a></div></div>
    <div class="row"><div><strong>監督:</strong></div><div><a>はんきゅう太郎</a></div></div>
  </div>
</div>
<div class="video-introduction-images-text"><p>夫の目の前で犯されてしまう。</p></div>
<span id="fullvideo_showallcg">
  <img data-src="/samples/ipz00950jp-1.jpg">
  <img data-src="/samples/ipz00950jp-2.jpg">
  <img data-src="/samples/ipz00950jp-3.jpg">
</span>
<script>
fullvideo_showallcg_arr=new Array();
fullvideo_showallcg_arr[3]='/samples/ipz00950jp-4.jpg';
fullvideo_showallcg_arr[4]='/samples/ipz00950jp-5.jpg';
</script>
</body></html>`, cover, title, number)
}

func TestMMTV_NormalizeAndParseURL(t *testing.T) {
	m := New()
	for input, want := range map[string]string{
		"ipz950":   "IPZ-950",
		"IPZ_950":  "IPZ-950",
		"SNOS 377": "SNOS-377",
		"ipz-950":  "IPZ-950",
	} {
		assert.Equal(t, want, m.NormalizeMovieID(input))
		assert.Equal(t, want, m.NormalizeMovieKeyword(input))
	}
	assert.Empty(t, m.NormalizeMovieID("083120_001"))

	for input, want := range map[string]string{
		"https://7mmtv.sx/ja/censored_content/45526/IPZ-950.html":                      "IPZ-950",
		"https://7mmtv.sx/zh/chinese_content/63238/SNOS-377.html":                      "SNOS-377",
		"https://7mmtv.sx/ja/searchall_search/all/IPZ-950/1.html":                      "IPZ-950",
		"https://7mmtv.sx/ja/searchform_search/all/index.html?search_keyword=SNOS-377": "SNOS-377",
	} {
		id, err := m.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://example.com/ja/censored_content/45526/IPZ-950.html",
		"https://7mmtv.sx/ja/censored_content/45526/",
		"file:///IPZ-950.html",
	} {
		_, err := m.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestMMTV_SearchMovieFiltersAndDeduplicates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		number    string
		cards     string
		wantID    string
		wantTitle string
	}{
		{
			name:   "IPZ-950 prefers censored content",
			number: "IPZ-950",
			cards: searchCard("reducing-mosaic_content", "17192", "IPZ-950", "[無碼破解]IPZ-950 related copy", "/reducing.webp", "2022-09-20") +
				searchCard("censored_content", "45526", "IPZ-950", "IPZ-950 IPZ-950 アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", "/censored.webp", "2017-05-22") +
				searchCard("censored_content", "45527", "IPZ-951", "IPZ-951 related", "/wrong.webp", "2017-06-01"),
			wantID:    "45526",
			wantTitle: "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな",
		},
		{
			name:   "SNOS-377 prefers censored content",
			number: "SNOS-377",
			cards: searchCard("chinese_content", "63238", "SNOS-377", "SNOS-377 chinese", "/chinese.webp", "2024-01-01") +
				searchCard("censored_content", "206038", "SNOS-377", "SNOS-377 censored", "/censored.webp", "2024-02-01") +
				searchCard("reducing-mosaic_content", "106870", "SNOS-377", "SNOS-377 reduced", "/reducing.webp", "2024-03-01") +
				searchCard("censored_content", "206039", "SNOS-378", "SNOS-378 related", "/wrong.webp", "2024-04-01"),
			wantID:    "206038",
			wantTitle: "censored",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/ja/searchall_search/all/"+tc.number+"/1.html", r.URL.Path)
				fmt.Fprint(w, `<div class="row content">`+tc.cards+`</div>`)
			})

			results, err := m.SearchMovie(tc.number)
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, tc.number, results[0].Number)
			assert.Equal(t, tc.wantTitle, results[0].Title)
			assert.Contains(t, results[0].Homepage, "/"+tc.wantID+"/")
			assert.Contains(t, results[0].Homepage, "/censored_content/")
			assert.True(t, results[0].IsValid())
		})
	}
}

func TestMMTV_GetMovieInfoByID(t *testing.T) {
	m := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/searchall_search/all/IPZ-950/1.html":
			fmt.Fprint(w, `<div class="row content">`+
				searchCard("reducing-mosaic_content", "17192", "IPZ-950", "Reduced copy", "/reducing.webp", "2022-09-20")+
				searchCard("censored_content", "45526", "IPZ-950", "IPZ-950 exact", "/search.webp", "2017-05-22")+
				`</div>`)
		case "/ja/censored_content/45526/IPZ-950.html":
			fmt.Fprint(w, detailPage(
				"IPZ-950",
				"IPZ-950 アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな",
				"/cover.webp",
			))
		default:
			http.NotFound(w, r)
		}
	})

	info, err := m.GetMovieInfoByID("ipz950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "IPZ-950", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな", info.Title)
	assert.Equal(t, "2017-05-20", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 118, info.Runtime)
	assert.Equal(t, "はんきゅう太郎", info.Director)
	assert.Equal(t, "アイデアポケット", info.Maker)
	assert.Equal(t, "ティッシュ", info.Label)
	assert.Equal(t, []string{"桃乃木かな"}, []string(info.Actors))
	assert.Equal(t, []string{"ドラマ", "人妻", "単体作品"}, []string(info.Genres))
	assert.Equal(t, "夫の目の前で犯されてしまう。", info.Summary)
	assert.Equal(t, m.URL().String()+"cover.webp", info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	require.Len(t, info.PreviewImages, 5)
	assert.Equal(t, m.URL().String()+"samples/ipz00950jp-5.jpg", info.PreviewImages[4])
}

func TestMMTV_GetMovieInfoByIDRelatedOnly(t *testing.T) {
	m := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<div class="row content">`+
			searchCard("censored_content", "900001", "SNOS-378", "Related", "/wrong.webp", "2024-04-01")+
			`</div>`)
	})

	info, err := m.GetMovieInfoByID("SNOS-377")
	assert.Nil(t, info)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestMMTV_CleanMovieTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		id    string
		want  string
	}{
		{
			title: "IPZ-950 IPZ-950 アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな",
			id:    "IPZ-950",
			want:  "アタッカーズ全面監修 夫の目の前で犯されて― 桃乃木かな",
		},
		{
			title: "SNOS-377 タイトル",
			id:    "SNOS-377",
			want:  "タイトル",
		},
		{
			title: "IPZ-9500 unrelated",
			id:    "IPZ-950",
			want:  "IPZ-9500 unrelated",
		},
	} {
		assert.Equal(t, tc.want, cleanMovieTitle(tc.title, tc.id))
	}
}

func TestMMTV_DetectPageError(t *testing.T) {
	validPage := `<html><head><title>IPZ-950 - 7mmtv.sx</title></head><body>
<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>
<div class="col-item">valid content</div>
</body></html>`
	assert.NoError(t, detectPageError([]byte(validPage)))

	for _, challenge := range []string{
		`<html><head><title>Just a moment...</title></head></html>`,
		`<html><head><title>Attention Required! | Cloudflare</title></head></html>`,
		`<html><body><div id="challenge-form"></div></body></html>`,
	} {
		assert.ErrorIs(t, detectPageError([]byte(challenge)), errAntiBotRequest)
	}
}

func TestMMTV_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		response := ""
		switch {
		case strings.Contains(payload.URL, "/searchall_search/"):
			response = `<div class="row content">` +
				searchCard("censored_content", "45526", "IPZ-950", "Solved search", "/solved.webp", "2017-05-22") +
				`</div>`
		case strings.Contains(payload.URL, "/censored_content/45526/IPZ-950.html"):
			response = detailPage("IPZ-950", "Solved detail", "/solved-cover.webp")
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
	m.Scraper = scraper.NewDefaultScraper(Name, target.URL+"/", Priority, language.Japanese)
	require.NoError(t, m.SetFlareSolverr(flare.URL+"/v1"))

	info, err := m.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "Solved detail", info.Title)
	assert.Equal(t, target.URL+"/solved-cover.webp", info.CoverURL)
}

func TestMMTV_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	m := New()
	m.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, m.SetFlareSolverr(endpoint))

	for _, number := range []string{"IPZ-950", "SNOS-377"} {
		t.Run(number, func(t *testing.T) {
			info, err := m.GetMovieInfoByID(number)
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
