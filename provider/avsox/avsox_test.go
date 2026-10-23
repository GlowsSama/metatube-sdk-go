package avsox

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/avcore"
)

func newTestAVSOX(t *testing.T, handler http.HandlerFunc) *AVSOX {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AVSOX{Provider: avcore.New(Name, server.URL+"/", Priority)}
}

func avsoxCard(detailID, title, number, date, cover string) string {
	return fmt.Sprintf(`<a class="movie-card shadow-2" href="/ja/movies/%s">
<img class="poster" src="%s" alt="%s">
<div class="movie-info">
<div class="movie-name"><span>%s</span></div>
<div class="movie-meta"><span>%s</span><span>%s</span></div>
</div>
</a>`, detailID, cover, title, title, number, date)
}

func avsoxDetailPage() string {
	return `<!doctype html><html><head>
<meta name="description" content="HEYZO-3993 detail summary">
</head><body>
<h1>HEYZO-3993 癒し系保育士 さゆりちゃんは容姿からは想像できないほどの敏感娘でした</h1>
<div class="q-img poster-image"><img src="https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993pl.jpg"></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">品番:</span><span class="detail-value detail-value--accent">HEYZO-3993</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">発売日:</span><span class="detail-value">2026-10-22</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">収録時間:</span><span class="detail-value">60分</span></div></div>
<div class="detail-item"><div class="detail-row"><span class="detail-label">監督:</span><span class="detail-value">-</span></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">メーカー:</span><a class="detail-value detail-text-link">HEYZO</a></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">レーベル:</span><span class="detail-value">-</span></div></div>
<div class="detail-item"><div class="detail-row detail-row--stacked"><span class="detail-label">シリーズ:</span><span class="detail-value">-</span></div></div>
<div class="detail-item detail-item--stacked"><div class="detail-row detail-row--links detail-row--stacked"><span class="detail-label">ジャンル:</span><span class="link-row">
<a class="detail-link">ハイビジョン</a><a class="detail-link">独占配信</a>
</span></div></div>
<section class="actresses"><div class="actress-name">原田さゆり</div></section>
<div class="sample-grid">
<img src="https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993-1.jpg">
<img src="https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993-2.jpg">
</div>
</body></html>`
}

func TestAVSOX_ParseMovieIDFromURL(t *testing.T) {
	av := New()
	for input, want := range map[string]string{
		"https://avsox.click/ja/movies/kjaobvw":       "kjaobvw",
		"https://www.avsox.click/ja/movies/kjaobvw":   "kjaobvw",
		"https://avsox.click/ja/search/HEYZO-3993":    "HEYZO-3993",
		"https://avsox.click/ja/search/heyzo3993?p=1": "HEYZO-3993",
	} {
		id, err := av.ParseMovieIDFromURL(input)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, input := range []string{
		"https://avmoo.shop/ja/movies/kjaobvw",
		"https://example.com/ja/movies/kjaobvw",
		"file:///ja/movies/kjaobvw",
	} {
		_, err := av.ParseMovieIDFromURL(input)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestAVSOX_SearchFiltersAndDeduplicates(t *testing.T) {
	av := newTestAVSOX(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ja/search/IPZ-950", r.URL.Path)
		fmt.Fprint(w,
			avsoxCard("avsoxid1", "IPZ-950 Exact title", "IPZ-950", "2017-05-20", "/exact.jpg")+
				avsoxCard("avsoxid2", "IPZZ-950 Related title", "IPZZ-950", "2024-01-01", "/related.jpg")+
				avsoxCard("avsoxid1", "IPZ-950 Duplicate title", "IPZ-950", "2017-05-20", "/duplicate.jpg"))
	})

	results, err := av.SearchMovie("ipz950.mkv")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "avsoxid1", results[0].ID)
	assert.Equal(t, "IPZ-950", results[0].Number)
	assert.Equal(t, "Exact title", results[0].Title)
	assert.Equal(t, "2017-05-20", time.Time(results[0].ReleaseDate).Format("2006-01-02"))
}

func TestAVSOX_GetMovieInfoByID(t *testing.T) {
	av := newTestAVSOX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ja/search/HEYZO-3993":
			fmt.Fprint(w, avsoxCard(
				"kjaobvw",
				"癒し系保育士 さゆりちゃんは容姿からは想像できないほどの敏感娘でした",
				"HEYZO-3993",
				"2026-10-22",
				"/thumb.jpg",
			))
		case "/ja/movies/kjaobvw":
			fmt.Fprint(w, avsoxDetailPage())
		default:
			http.NotFound(w, r)
		}
	})

	info, err := av.GetMovieInfoByID("HEYZO3993")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "kjaobvw", info.ID)
	assert.Equal(t, "HEYZO-3993", info.Number)
	assert.Equal(t, "癒し系保育士 さゆりちゃんは容姿からは想像できないほどの敏感娘でした", info.Title)
	assert.Equal(t, "2026-10-22", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Equal(t, 60, info.Runtime)
	assert.Equal(t, "HEYZO", info.Maker)
	assert.Equal(t, []string{"ハイビジョン", "独占配信"}, []string(info.Genres))
	assert.Equal(t, []string{"原田さゆり"}, []string(info.Actors))
	assert.Equal(t, "https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993pl.jpg", info.CoverURL)
	assert.Equal(t, []string{
		"https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993-1.jpg",
		"https://jp.netcdn.space/digital/video/heyzo003993/heyzo003993-2.jpg",
	}, []string(info.PreviewImages))
	assert.Empty(t, info.Summary)
}
