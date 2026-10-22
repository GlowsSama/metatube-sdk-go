package javbus

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/testkit"
)

func TestJavBus_GetMovieInfoByID(t *testing.T) {
	testkit.Test(t, New, []string{
		"SMBD-77",
		"SSNI-776",
		"ABP-331",
		"CEMD-232",
		"th101-000-110942",
	})
}

func TestJavBus_SearchMovie(t *testing.T) {
	testkit.Test(t, New, []string{
		"SSIS-033",
		"MIDV-005",
		// Detail page exists, but is missing from JavBus's search index.
		"RBD-859",
		// Missing the dash between label and number.
		"ssis277",
	})
}

func TestJavBus_Normalize(t *testing.T) {
	bus := New()
	for keyword, want := range map[string]string{
		"ssis277":     "SSIS-277",
		"MIDV005":     "MIDV-005",
		"SSIS-277":    "SSIS-277",
		"ssis-033":    "SSIS-033",
		"083120_001":  "083120_001",
		"heyzo-1234":  "HEYZO-1234",
		"FC2-1234567": "", // JavBus has no FC2 content.
	} {
		require.Equal(t, want, bus.NormalizeMovieKeyword(keyword))
	}
	for id, want := range map[string]string{
		"ssis277":             "SSIS-277",
		"SSIS-277":            "SSIS-277",
		"MIDV-005_2025-02-04": "MIDV-005_2025-02-04",
		"n1234":               "N1234", // Single-letter label, keep as is.
	} {
		require.Equal(t, want, bus.NormalizeMovieID(id))
	}
}

func TestJavBus_FilterSearchResults(t *testing.T) {
	results := []*model.MovieSearchResult{
		{
			ID:       "ipz950",
			Number:   "IPZ-950",
			Title:    "Exact result",
			Provider: Name,
			Homepage: baseURL + "ja/ipz950",
		},
		{
			ID:       "ipzz950",
			Number:   "IPZZ-950",
			Title:    "Related result",
			Provider: Name,
			Homepage: baseURL + "ja/ipzz950",
		},
		{
			ID:       "ipz950",
			Number:   "IPZ-950",
			Title:    "Duplicate result",
			Provider: Name,
			Homepage: baseURL + "ja/ipz950",
		},
	}

	filtered := New().filterSearchResults(results, "IPZ-950")
	require.Len(t, filtered, 1)
	require.Equal(t, "IPZ-950", filtered[0].Number)
	require.Equal(t, "Exact result", filtered[0].Title)
}

func TestJavBus_GetMovieInfoByURL_English(t *testing.T) {
	const page = `<!doctype html>
<html><body>
<a class="bigImage"><img src="/pics/cover/85ww_b.jpg" title="English title"></a>
<div class="col-md-3 info">
  <p><span class="header">ID:</span> <span>SSIS-033</span></p>
  <p><span class="header">Release Date:</span> 2021-04-02</p>
  <p><span class="header">Length:</span> 170min</p>
  <p><span class="header">Director:</span> <a>Director</a></p>
  <p><span class="header">Studio:</span> <a>Studio</a></p>
  <p><span class="header">Label:</span> <a>Label</a></p>
  <p><span class="header">Series:</span> <a>Series</a></p>
  <span class="genre"><label><a>Genre</a></label></span>
</div>
<div class="star-name"><a title="Actor">Actor</a></div>
<div id="sample-waterfall"><a href="/sample.jpg"></a></div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(server.URL + "/en/SSIS-033")
	require.NoError(t, err)
	require.Equal(t, "SSIS-033", info.Number)
	require.Equal(t, "English title", info.Title)
	require.Equal(t, parser.ParseDate("2021-04-02"), info.ReleaseDate)
	require.Equal(t, 170, info.Runtime)
	require.Equal(t, "Director", info.Director)
	require.Equal(t, "Studio", info.Maker)
	require.Equal(t, "Label", info.Label)
	require.Equal(t, "Series", info.Series)
	require.ElementsMatch(t, []string{"Genre"}, info.Genres)
	require.ElementsMatch(t, []string{"Actor"}, info.Actors)
}

func TestJavBus_GetMovieInfoByURL_VerificationRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/doc/driver-verify" {
			_, _ = w.Write([]byte(`<div id="ageVerify"></div>`))
			return
		}
		http.Redirect(w, r, "/doc/driver-verify", http.StatusFound)
	}))
	defer server.Close()

	info, err := New().GetMovieInfoByURL(server.URL + "/ja/SSIS-033")
	require.ErrorIs(t, err, errVerificationRequired)
	require.Nil(t, info)
}
