package avmoo

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

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/avcore"
)

func newTestAVMoo(t *testing.T, handler http.HandlerFunc) *AVMoo {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AVMoo{Provider: avcore.New(Name, server.URL+"/", Priority)}
}

func avmooCard(detailID, title, number, date, cover string) string {
	return fmt.Sprintf(`<a class="movie-card shadow-2" href="/ja/movies/%s">
<img class="poster" src="%s" alt="%s">
<div class="movie-info">
<div class="movie-name"><span>%s</span></div>
<div class="movie-meta"><span>%s</span><span>%s</span></div>
</div>
</a>`, detailID, cover, title, title, number, date)
}

func avmooDetailPage() string {
	return `<!doctype html><html><head>
<meta name="description" content="AVMOO - Your online informative source for Japanese adult videos">
<meta name="description" content="IPZ-950 detail summary" data-qmeta="description">
</head><body>
<h1>IPZ-950 アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな</h1>
<div class="q-img poster-image"><img src="https://jp.netcdn.space/digital/video/ipz00950/ipz00950pl.jpg"></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">品番:</span><span class="detail-value detail-value--accent">IPZ-950</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">発売日:</span><span class="detail-value">2017-05-20</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">収録時間:</span><span class="detail-value">118分</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">監督:</span><a class="detail-value detail-text-link">はんきゅう太郎</a></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">メーカー:</span><a class="detail-value detail-text-link">アイデアポケット</a></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">レーベル:</span><a class="detail-value detail-text-link">ティッシュ</a></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">シリーズ:</span><a class="detail-value detail-text-link">夫の目の前で犯●れて</a></div></div>
<div class="detail-item detail-item--stacked"><div class="detail-row detail-row--links detail-row--stacked"><span class="detail-label">ジャンル:</span><span class="link-row">
<a class="detail-link">人妻・主婦</a><a class="detail-link">単体作品</a><a class="detail-link">独占配信</a>
<a class="detail-link">デジモ</a><a class="detail-link">ハイビジョン</a><a class="detail-link">ドラマ</a>
</span></div></div>
<section class="actresses"><div class="actress-name">桃乃木かな</div></section>
<div class="sample-grid">
<img src="https://jp.netcdn.space/digital/video/ipz00950/ipz00950-1.jpg">
<img src="https://jp.netcdn.space/digital/video/ipz00950/ipz00950-2.jpg">
</div>
</body></html>`
}

func TestAVMoo_ParseMovieIDFromURL(t *testing.T) {
	av := New()
	for input, want := range map[string]string{
		"https://avmoo.shop/ja/movies/kolvwal":       "kolvwal",
		"https://www.avmoo.shop/ja/movies/kolvwal":   "kolvwal",
		"https://avmoo.shop/ja/search/IPZ-950":       "IPZ-950",
		"https://avmoo.shop/ja/search/ipz950?page=1": "IPZ-950",
	} {
		id, err := av.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://avsox.click/ja/movies/kolvwal",
		"https://example.com/ja/movies/kolvwal",
		"file:///ja/movies/kolvwal",
	} {
		_, err := av.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestAVMoo_SearchFiltersAndDeduplicates(t *testing.T) {
	av := newTestAVMoo(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search/IPZ-950", r.URL.Path)
		fmt.Fprint(w,
			avmooCard("kolvwal", "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", "IPZ-950", "2017-05-20", "/exact.jpg")+
				avmooCard("related", "IPZZ-950 Related title", "IPZZ-950", "2024-01-01", "/related.jpg")+
				avmooCard("kolvwal", "Duplicate title", "IPZ-950", "2017-05-20", "/duplicate.jpg"))
	})

	results, err := av.SearchMovie("ipz950.mkv")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "kolvwal", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", results[0].Title)
	assert.Equal(t, "2017-05-20", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
}

func TestAVMoo_GetMovieInfoByID(t *testing.T) {
	av := newTestAVMoo(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search/IPZ-950":
			fmt.Fprint(w, avmooCard(
				"kolvwal",
				"アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな",
				"IPZ-950",
				"2017-05-20",
				"/thumb.jpg",
			))
		case "/ja/movies/kolvwal":
			fmt.Fprint(w, avmooDetailPage())
		default:
			http.NotFound(w, r)
		}
	})

	info, err := av.GetMovieInfoByID("IPZ950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "kolvwal", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", info.Title)
	assert.Equal(t, "2017-05-20", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 118, info.Runtime)
	assert.Equal(t, "はんきゅう太郎", info.Director)
	assert.Equal(t, "アイデアポケット", info.Maker)
	assert.Equal(t, "ティッシュ", info.Label)
	assert.Equal(t, "夫の目の前で犯●れて", info.Series)
	assert.Equal(t, []string{"人妻・主婦", "単体作品", "独占配信", "デジモ", "ハイビジョン", "ドラマ"}, []string(info.Genres))
	assert.Equal(t, []string{"桃乃木かな"}, []string(info.Actors))
	assert.Equal(t, "https://jp.netcdn.space/digital/video/ipz00950/ipz00950pl.jpg", info.CoverURL)
	assert.Equal(t, []string{
		"https://jp.netcdn.space/digital/video/ipz00950/ipz00950-1.jpg",
		"https://jp.netcdn.space/digital/video/ipz00950/ipz00950-2.jpg",
	}, []string(info.PreviewImages))
}

func TestAVMoo_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
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
			response = avmooCard(
				"kolvwal",
				"アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな",
				"IPZ-950",
				"2017-05-20",
				"/solved-thumb.jpg",
			)
		case strings.Contains(payload.URL, "/ja/movies/kolvwal"):
			response = avmooDetailPage()
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

	av := &AVMoo{Provider: avcore.New(Name, target.URL+"/", Priority)}
	require.NoError(t, av.SetFlareSolverr(flare.URL+"/v1"))

	info, err := av.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "アタッカーズ全面監修 夫の目の前で犯●れて― 桃乃木かな", info.Title)
	assert.Equal(t, "https://jp.netcdn.space/digital/video/ipz00950/ipz00950pl.jpg", info.CoverURL)
}

func TestAVMoo_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	av := New()
	av.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, av.SetFlareSolverr(endpoint))

	for _, number := range []string{"IPZ-950", "SNOS-377"} {
		t.Run(number, func(t *testing.T) {
			info, err := av.GetMovieInfoByID(number)
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
