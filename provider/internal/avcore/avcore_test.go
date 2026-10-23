package avcore

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

const testPriority = 990

func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New("AVTest", server.URL+"/", testPriority)
}

func searchCard(detailID, title, cover string) string {
	return fmt.Sprintf(`<a class="movie-card" href="/ja/movies/%s">
<img data-src="%s">
<div class="movie-name">%s</div>
<div class="movie-meta"><span>2017-05-22</span></div>
</a>`, detailID, cover, title)
}

func detailPage(title string) string {
	return fmt.Sprintf(`<!doctype html><html><head>
<meta name="description" content="Detail summary">
</head><body>
<h1>%s</h1>
<div class="q-img poster-image"><img data-src="/cover.jpg"></div>
<div class="detail-item"><span class="detail-label">品番</span><span class="detail-value">IPZ-950</span></div>
<div class="detail-item"><span class="detail-label">発売日</span><span class="detail-value">2017-05-22</span></div>
<div class="detail-item"><span class="detail-label">収録時間</span><span class="detail-value">150 分</span></div>
<div class="detail-item"><span class="detail-label">監督</span><span class="detail-value"><a>Director</a></span></div>
<div class="detail-item"><span class="detail-label">メーカー</span><span class="detail-value"><a>Maker</a></span></div>
<div class="detail-item"><span class="detail-label">レーベル</span><span class="detail-value"><a>Label</a></span></div>
<div class="detail-item"><span class="detail-label">シリーズ</span><span class="detail-value"><a>Series</a></span></div>
<div class="detail-item"><span class="detail-label">ジャンル</span><span class="detail-value"><a>単体作品</a><a>人妻</a></span></div>
<div class="actress-name">Actor A</div>
<div class="sample-grid"><img data-src="/sample.jpg"></div>
</body></html>`, title)
}

func TestProviderParseMovieIDFromURL(t *testing.T) {
	av := New("AVTest", "http://127.0.0.1:12345/", testPriority)
	id, err := av.ParseMovieIDFromURL("http://127.0.0.1:12345/ja/movies/javlikjz7a")
	require.NoError(t, err)
	assert.Equal(t, "javlikjz7a", id)
}

func TestProviderSearchFiltersAndDeduplicates(t *testing.T) {
	av := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search/IPZ-950", r.URL.Path)
		fmt.Fprint(w,
			searchCard("javlikjz7a", "IPZ-950 Exact", "/exact.jpg")+
				searchCard("javlikjz7b", "IPZZ-950 Related", "/related.jpg")+
				searchCard("javlikjz7a", "IPZ-950 Duplicate", "/duplicate.jpg"))
	})

	results, err := av.SearchMovie("ipz950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "javlikjz7a", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Exact", results[0].Title)
}

func TestProviderDetailParsing(t *testing.T) {
	av := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/movies/javlikjz7a", r.URL.Path)
		fmt.Fprint(w, detailPage("IPZ-950 Detail title"))
	})

	base := strings.TrimSuffix(av.URL().String(), "/")
	info, err := av.GetMovieInfoByURL(base + "/ja/movies/javlikjz7a")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "javlikjz7a", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, "Detail title", info.Title)
	assert.Equal(t, "2017-05-22", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 150, info.Runtime)
	assert.Equal(t, "Director", info.Director)
	assert.Equal(t, "Maker", info.Maker)
	assert.Equal(t, "Label", info.Label)
	assert.Equal(t, "Series", info.Series)
	assert.Equal(t, []string{"単体作品", "人妻"}, []string(info.Genres))
	assert.Equal(t, []string{"Actor A"}, []string(info.Actors))
	assert.Equal(t, base+"/cover.jpg", info.CoverURL)
	assert.Equal(t, []string{base + "/sample.jpg"}, []string(info.PreviewImages))
}

func TestProviderUsesFlareSolverrForCloudflareChallenge(t *testing.T) {
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
			response = searchCard("javlikjz7a", "IPZ-950 Solved", "/solved.jpg")
		case strings.Contains(payload.URL, "/ja/movies/javlikjz7a"):
			response = detailPage("IPZ-950 Solved detail")
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

	av := New("AVTest", target.URL+"/", testPriority)
	av.Scraper = scraper.NewDefaultScraper("AVTest", target.URL+"/", testPriority, language.Japanese)
	require.NoError(t, av.SetFlareSolverr(flare.URL+"/v1"))

	info, err := av.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "Solved detail", info.Title)
	assert.Equal(t, target.URL+"/cover.jpg", info.CoverURL)
}

func TestProviderRendersSPAShellWithFlareSolverr(t *testing.T) {
	const shell = `<!doctype html><html><body>
<div id="jav-site-index" data-v-app=""></div>
<script src="/res/js/jav.min.js"></script>
</body></html>`

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, shell)
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
			response = searchCard("javlikjz7a", "IPZ-950 Rendered", "/rendered.jpg")
		case strings.Contains(payload.URL, "/ja/movies/javlikjz7a"):
			response = detailPage("IPZ-950 Rendered detail")
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

	av := New("AVTest", target.URL+"/", testPriority)
	require.NoError(t, av.SetFlareSolverr(flare.URL+"/v1"))

	info, err := av.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "Rendered detail", info.Title)
	assert.Equal(t, target.URL+"/cover.jpg", info.CoverURL)
}

func TestProviderRejectsForeignURL(t *testing.T) {
	av := New("AVTest", "https://example.test/", testPriority)
	_, err := av.ParseMovieIDFromURL("https://not-example.test/ja/movies/javlikjz7a")
	assert.ErrorIs(t, err, provider.ErrInvalidURL)
}
