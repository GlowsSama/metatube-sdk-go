package missav

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

func newTestMissAV(t *testing.T, handler http.HandlerFunc) *MissAV {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m := New()
	m.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/", Priority, language.Japanese)
	return m
}

func missAVCard(href, title, cover string) string {
	return fmt.Sprintf(`<div class="thumbnail group">
<a class="thumbnail" href="%s"><img data-src="%s"></a>
<div class="my-2"><a href="%s">%s</a></div>
</div>`, href, cover, href, title)
}

func missAVDetailPage() string {
	return `<!doctype html><html><head>
<meta name="description" content="Truncated description">
<meta property="og:description" content="MissAV detail summary">
<meta property="og:image" content="https://fourhoi.com/ipz-950/cover-n.jpg">
<meta property="og:video:duration" content="7080">
</head><body>
<nav><a href="/ja/actresses/ranking">女優ランキング</a> <a href="/ja/genres/vr">VR</a></nav>
<h1>IPZ-950 アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな</h1>
<div class="text-secondary break-all">愛理は俊之と結婚し主婦として充実した毎日を送っていた。</div>
<div class="text-secondary"><span>配信開始日:</span> <time datetime="2017-05-20T12:00:52+08:00">2017-05-20</time></div>
<div class="text-secondary"><span>品番:</span> <span>IPZ-950</span></div>
<div class="text-secondary"><span>女優:</span> <a href="/dm194/ja/actresses/momonogi">桃乃木かな</a></div>
<div class="text-secondary"><span>ジャンル:</span> <a href="/dm81/ja/genres/wife">人妻・主婦</a>, <a href="/dm122/ja/genres/solo">単体作品</a>, <a href="/dm106/ja/genres/drama">ドラマ</a>, <a href="/dm77/ja/genres/digimo">デジモ</a>, <a href="/dm96/ja/genres/hd">ハイビジョン</a>, <a href="/dm142/ja/genres/exclusive">独占配信</a></div>
<div class="text-secondary"><span>シリーズ:</span> <a href="/dm2/ja/series/series">夫の目の前で犯●れて</a></div>
<div class="text-secondary"><span>メーカー:</span> <a href="/dm573/ja/makers/idea">アイデアポケット</a></div>
<div class="text-secondary"><span>監督:</span> <a href="/ja/directors/hankyu">はんきゅう太郎</a></div>
<div class="text-secondary"><span>レーベル:</span> <a href="/dm83/ja/labels/tissue">ティッシュ</a></div>
<video class="player" src="blob:https://missav.ws/example"></video>
</body></html>`
}

func TestMissAV_ParseMovieIDFromURL(t *testing.T) {
	m := New()
	for input, want := range map[string]string{
		"https://missav.ws/dm102/ja/ipz-950":   "IPZ-950",
		"https://missav.ws/ja/search/SNOS-377": "SNOS-377",
		"https://missav.ai/dm102/ja/ipz-950":   "IPZ-950",
	} {
		id, err := m.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://missav.ws/dm25/ja/ipz-950-uncensored-leak",
		"https://missav.ws/ja/ipz-950-english-subtitle",
		"https://example.com/ja/ipz-950",
	} {
		_, err := m.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestMissAV_SearchFiltersVariantsAndDuplicates(t *testing.T) {
	m := newTestMissAV(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search/IPZ-950", r.URL.Path)
		fmt.Fprint(w,
			missAVCard("/dm25/ja/ipz-950-uncensored-leak", "IPZ-950 Uncensored Leak", "/leak.jpg")+
				missAVCard("/dm102/ja/ipz-950", "IPZ-950 Exact title", "/exact.jpg")+
				missAVCard("/ja/ipz-950-english-subtitle", "IPZ-950 English subtitle", "/sub.jpg")+
				missAVCard("/dm102/ja/ipz-950", "IPZ-950 Duplicate title", "/duplicate.jpg")+
				missAVCard("/dm99/ja/ipzz-950", "IPZZ-950 Related title", "/related.jpg"))
	})

	results, err := m.SearchMovie("ipz-950.mkv")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-950", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Exact title", results[0].Title)
}

func TestMissAV_GetMovieInfoByID(t *testing.T) {
	m := newTestMissAV(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search/IPZ-950":
			fmt.Fprint(w, missAVCard("/dm102/ja/ipz-950", "IPZ-950 Search title", "/thumb.jpg"))
		case "/dm102/ja/ipz-950":
			fmt.Fprint(w, missAVDetailPage())
		default:
			http.NotFound(w, r)
		}
	})

	info, err := m.GetMovieInfoByID("IPZ950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "IPZ-950", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", info.Title)
	assert.Equal(t, "2017-05-20", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, []string{"桃乃木かな"}, []string(info.Actors))
	assert.Equal(t,
		[]string{"人妻・主婦", "単体作品", "ドラマ", "デジモ", "ハイビジョン", "独占配信"},
		[]string(info.Genres))
	assert.Equal(t, "夫の目の前で犯●れて", info.Series)
	assert.Equal(t, "アイデアポケット", info.Maker)
	assert.Equal(t, "はんきゅう太郎", info.Director)
	assert.Equal(t, "ティッシュ", info.Label)
	assert.Equal(t, 118, info.Runtime)
	assert.Equal(t, "https://fourhoi.com/ipz-950/cover-n.jpg", info.CoverURL)
	assert.Equal(t, "https://fourhoi.com/ipz-950/preview.mp4", info.PreviewVideoURL)
	assert.Equal(t, "愛理は俊之と結婚し主婦として充実した毎日を送っていた。", info.Summary)
}

func TestMissAV_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
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
		case strings.Contains(payload.URL, "/ja/search/IPZ-950"):
			response = missAVCard("/dm102/ja/ipz-950", "IPZ-950 Solved search", "/solved-thumb.jpg")
		case strings.Contains(payload.URL, "/dm102/ja/ipz-950"):
			response = missAVDetailPage()
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
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", info.Title)
	assert.Equal(t, "https://fourhoi.com/ipz-950/cover-n.jpg", info.CoverURL)
}
