package javlibrary

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

func newTestProvider(t *testing.T, handler http.HandlerFunc) *JavLibrary {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	jl := New()
	jl.Fetcher = fetch.Default(&fetch.Config{Referer: server.URL + "/"})
	jl.Scraper = scraper.NewDefaultScraper(Name, server.URL+"/", Priority, language.Japanese)
	return jl
}

func searchCard(detailID, number, title, cover string) string {
	return fmt.Sprintf(`<div class="video">
<a href="/ja/%s.html" title="%s"><img data-src="%s"></a>
<div class="id">%s</div>
<div class="title"><a href="/ja/%s.html">%s</a></div>
</div>`, detailID, title, cover, number, detailID, title)
}

func detailPage(number, title, cover string) string {
	return fmt.Sprintf(`<!doctype html><html><head>
<meta property="og:image" content="/fallback.jpg">
</head><body>
<div id="video_title"><h3>%s</h3></div>
<div id="video_id"><span class="text">%s</span></div>
<div id="video_date"><span class="text">2017-05-25</span></div>
<div id="video_length"><span class="text">120</span> 分</div>
<div id="video_director"><span class="text"><a>はんきゅう太郎</a></span></div>
<div id="video_maker"><span class="text"><a>アイデアポケット</a></span></div>
<div id="video_label"><span class="text"><a>ティッシュ</a></span></div>
<div id="video_series"><span class="text"><a>夫の目の前で犯されて</a></span></div>
<div id="video_genres"><a>単体作品</a><a>人妻</a><a>調教</a><a>人妻</a></div>
<div id="video_cast"><span class="star"><a>桃乃木かな</a></span></div>
<div id="video_review"><span class="score">8.60点</span></div>
<img id="video_jacket_img" data-src="%s">
</body></html>`, title, number, cover)
}

func TestJavLibrary_NormalizeAndParseURL(t *testing.T) {
	jl := New()
	for input, want := range map[string]string{
		"ipz950":   "IPZ-950",
		"IPZ_950":  "IPZ-950",
		"SNOS 377": "SNOS-377",
		"ipz-950":  "IPZ-950",
	} {
		assert.Equal(t, want, jl.NormalizeMovieKeyword(input))
	}
	assert.Equal(t, "javlikjz7a", jl.NormalizeMovieID("javlikjz7a.html"))
	assert.Equal(t, "IPZ-950", jl.NormalizeMovieID("ipz-950"))

	for input, want := range map[string]string{
		"https://www.javlibrary.com/ja/vl_searchbyid.php?keyword=IPZ-950": "IPZ-950",
		"https://www.javlibrary.com/ja/javlikjz7a.html":                   "javlikjz7a",
	} {
		id, err := jl.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://example.com/ja/javlikjz7a.html",
		"https://www.javlibrary.com/ja/vl_searchbyid.php",
		"https://www.javlibrary.com/ja/short.html",
		"file:///javlikjz7a.html",
	} {
		_, err := jl.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestJavLibrary_SearchMovieFiltersAndDeduplicates(t *testing.T) {
	jl := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/vl_searchbyid.php", r.URL.Path)
		assert.Equal(t, "IPZ-950", r.URL.Query().Get("keyword"))
		fmt.Fprint(w, searchCard("javlikjz7a", "IPZ-950", "Exact result", "/exact.jpg")+
			searchCard("javlikjz7b", "IPZZ-950", "Related result", "/related.jpg")+
			searchCard("javlikjz7a", "IPZ-950", "Duplicate result", "/duplicate.jpg"))
	})

	results, err := jl.SearchMovie("ipz950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "javlikjz7a", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Exact result", results[0].Title)
	assert.Equal(t, jl.URL().String()+"ja/javlikjz7a.html", results[0].Homepage)
}

func TestJavLibrary_SearchRedirectsToDetailPage(t *testing.T) {
	jl := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/vl_searchbyid.php":
			http.Redirect(w, r, "/ja/javlikjz7a.html", http.StatusFound)
		case "/ja/javlikjz7a.html":
			fmt.Fprint(w, detailPage("IPZ-950", "Exact detail title", "/cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	})

	results, err := jl.SearchMovie("IPZ-950")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "javlikjz7a", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Exact detail title", results[0].Title)
	assert.Contains(t, results[0].Homepage, "/ja/javlikjz7a.html")
}

func TestJavLibrary_GetMovieInfoByID(t *testing.T) {
	jl := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/vl_searchbyid.php":
			fmt.Fprint(w, searchCard("javlikjz7a", "IPZ-950", "Search title", "/search.jpg"))
		case "/ja/javlikjz7a.html":
			fmt.Fprint(w, detailPage("IPZ-950", "IPZ-950 Detail title", "/covers/ipz950.jpg"))
		default:
			http.NotFound(w, r)
		}
	})

	info, err := jl.GetMovieInfoByID("ipz950")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "javlikjz7a", info.ID)
	assert.Equal(t, "IPZ-950", info.Number)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, "Detail title", info.Title)
	assert.Equal(t, "2017-05-25", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 120, info.Runtime)
	assert.Equal(t, "はんきゅう太郎", info.Director)
	assert.Equal(t, "アイデアポケット", info.Maker)
	assert.Equal(t, "ティッシュ", info.Label)
	assert.Equal(t, "夫の目の前で犯されて", info.Series)
	assert.Equal(t, []string{"単体作品", "人妻", "調教"}, []string(info.Genres))
	assert.Equal(t, []string{"桃乃木かな"}, []string(info.Actors))
	assert.Equal(t, 8.6, info.Score)
	assert.Equal(t, jl.URL().String()+"covers/ipz950.jpg", info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	assert.Contains(t, info.Homepage, "/ja/javlikjz7a.html")
}

func TestJavLibrary_SearchNoResults(t *testing.T) {
	jl := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body>ご指定の検索条件に合う項目がありませんでした</body></html>`)
	})

	results, err := jl.SearchMovie("IPZ-950")
	assert.Nil(t, results)
	assert.ErrorIs(t, err, provider.ErrInfoNotFound)
}

func TestJavLibrary_DetectPageError(t *testing.T) {
	validPage := `<html><head><title>IPZ-950 - JAVLibrary</title></head><body>
<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>
<div id="video_id"><span class="text">IPZ-950</span></div>
</body></html>`
	assert.NoError(t, detectPageError([]byte(validPage)))

	for _, challenge := range []string{
		`<html><head><title>Just a moment...</title></head></html>`,
		`<html><body><div id="challenge-form"></div></body></html>`,
		`<html><body><script>window.__cf_chl_opt = {};</script></body></html>`,
	} {
		assert.ErrorIs(t, detectPageError([]byte(challenge)), errAntiBotChallenge)
	}
}

func TestJavLibrary_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1", r.URL.Path)
		var payload struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))

		response := ""
		switch {
		case strings.Contains(payload.URL, "/vl_searchbyid.php"):
			response = searchCard("javlikjz7a", "IPZ-950", "Solved search", "/solved-search.jpg")
		case strings.Contains(payload.URL, "/javlikjz7a.html"):
			response = detailPage("IPZ-950", "Solved detail", "/solved-cover.jpg")
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

	jl := New()
	jl.Scraper = scraper.NewDefaultScraper(Name, target.URL+"/", Priority, language.Japanese)
	require.NoError(t, jl.SetFlareSolverr(flare.URL+"/v1"))

	info, err := jl.GetMovieInfoByID("IPZ-950")
	require.NoError(t, err)
	assert.Equal(t, "Solved detail", info.Title)
	assert.Equal(t, target.URL+"/solved-cover.jpg", info.CoverURL)
}

func TestJavLibrary_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	jl := New()
	jl.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, jl.SetFlareSolverr(endpoint))

	for _, number := range []string{"IPZ-950", "SNOS-377"} {
		t.Run(number, func(t *testing.T) {
			info, err := jl.GetMovieInfoByID(number)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.True(t, info.IsValid())
			assert.Equal(t, number, info.Number)
			assert.NotEmpty(t, info.Title)
			assert.NotEmpty(t, info.CoverURL)
			assert.NotEmpty(t, info.ReleaseDate)
			t.Logf("title=%q release=%s runtime=%d actors=%v cover=%s",
				info.Title,
				time.Time(info.ReleaseDate).Format("2006-01-02"),
				info.Runtime,
				info.Actors,
				info.CoverURL,
			)
		})
	}
}
