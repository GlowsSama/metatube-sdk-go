package javdb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

func TestAppAPISignature(t *testing.T) {
	signature := appAPISignature(1_700_000_000)
	parts := strings.Split(signature, ".")
	require.Len(t, parts, 3)
	assert.Equal(t, "1700000000", parts[0])
	assert.Equal(t, "lpw6vgqzsp", parts[1])
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{32}$`), parts[2])
}

func TestJavDBAppAPI_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/search", r.URL.Path)
		assert.Equal(t, "HEYZO-0258", r.URL.Query().Get("q"))
		assert.Equal(t, "1", r.URL.Query().Get("page"))
		assert.Equal(t, "100", r.URL.Query().Get("limit"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.NotEmpty(t, r.Header.Get("jdsignature"))
		assert.NotEmpty(t, r.URL.Query().Get("device_uuid"))
		fmt.Fprint(w, `{"success":1,"data":{"movies":[
			{"id":"AbC123","number":"HEYZO-258","origin_title":"App API title",
			 "cover_url":"https://img.example/cover.jpg","thumb_url":"https://img.example/thumb.jpg",
			 "release_date":"2026-08-01","score":"4.6"}
		]}}`)
	}))
	t.Cleanup(server.Close)

	api, err := newJavDBAppAPI("test-token", server.URL+"/api", nil)
	require.NoError(t, err)
	results, err := api.search(t.Context(), "HEYZO-0258")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "AbC123", results[0].ID)
	assert.Equal(t, "HEYZO-258", results[0].Number)
	assert.Equal(t, "App API title", results[0].Title)
	assert.InDelta(t, 4.6, results[0].Score, 0.001)
}

func TestJavDBAppAPI_DetailNestedMovie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/movies/AbC123", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"success":true,"data":{"movie":{
			"id":"AbC123","number":"HEYZO-258","origin_title":"Original title","title":"Localized title",
			"summary":"Example summary","director_name":"Director A","maker_name":"Maker A",
			"publisher_name":"Publisher A","series_name":"Series A","release_date":"2026-08-01",
			"duration":120,"cover_url":"https://img.example/cover.jpg","preview_video_url":"https://video.example/preview.mp4",
			"score":4.7,"tags":[{"name":"Genre A"},{"name":"Genre B"}],
			"actors":[{"name":"Actor A","gender":0},{"name":"Actor B","gender":1}],
			"preview_images":[{"thumb_url":"/samples/1-thumb.jpg","large_url":"/samples/1.jpg"}]
		}}}`)
	}))
	t.Cleanup(server.Close)

	api, err := newJavDBAppAPI("test-token", server.URL+"/api", nil)
	require.NoError(t, err)
	info, err := api.movieInfo(t.Context(), "AbC123")
	require.NoError(t, err)
	assert.Equal(t, "AbC123", info.ID)
	assert.Equal(t, "HEYZO-258", info.Number)
	assert.Equal(t, "Original title", info.Title)
	assert.Equal(t, "Example summary", info.Summary)
	assert.ElementsMatch(t, []string{"Actor A"}, info.Actors)
	assert.ElementsMatch(t, []string{"Genre A", "Genre B"}, info.Genres)
	assert.Equal(t, 120, info.Runtime)
	assert.Equal(t, server.URL+"/samples/1.jpg", info.PreviewImages[0])
}

func TestJavDB_AppAPITriggersWithoutBackupDomains(t *testing.T) {
	var primaryHits, apiHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body>
<form action="/login"><input type="password" name="password"></form></body></html>`)
	}))
	t.Cleanup(primary.Close)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiHits++
		fmt.Fprint(w, `{"success":1,"data":{"movie":{
			"id":"AbC123","number":"SONE-065","origin_title":"App API title",
			"cover_url":"https://img.example/cover.jpg","release_date":"2026-08-01"
		}}}`)
	}))
	t.Cleanup(apiServer.Close)

	jav := New()
	primaryURL := strings.Replace(primary.URL, "127.0.0.1", "localhost", 1)
	jav.Scraper = scraper.NewDefaultScraper(Name, primaryURL, Priority, language.Chinese)
	_, err := jav.ConfigureSessionWithAPI("", "", "", "test-token", apiServer.URL+"/api")
	require.NoError(t, err)

	info, err := jav.GetMovieInfoByID("AbC123")
	require.NoError(t, err)
	assert.Equal(t, "App API title", info.Title)
	assert.Equal(t, 1, primaryHits)
	assert.Equal(t, 1, apiHits)
}

func TestJavDB_AppAPIDoesNotHandleOrdinaryFailures(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantApp int32
	}{
		{name: "not found", status: http.StatusNotFound, body: "not found"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: "rate limited"},
		{name: "server error", status: http.StatusInternalServerError, body: "server error"},
		{
			name: "cloudflare challenge", status: http.StatusForbidden,
			body: `<html><head><title>Just a moment...</title></head></html>`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var appHits atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			t.Cleanup(primary.Close)

			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				appHits.Add(1)
				fmt.Fprint(w, `{"success":1,"data":{"movie":{"id":"AbC123","number":"SONE-065",
					"origin_title":"Unexpected App API title"}}}`)
			}))
			t.Cleanup(apiServer.Close)

			jav := New()
			primaryURL := strings.Replace(primary.URL, "127.0.0.1", "localhost", 1)
			jav.Scraper = scraper.NewDefaultScraper(Name, primaryURL, Priority, language.Chinese)
			_, err := jav.ConfigureSessionWithAPI("", "", "", "test-token", apiServer.URL+"/api")
			require.NoError(t, err)

			_, err = jav.GetMovieInfoByID("AbC123")
			require.Error(t, err)
			assert.Zero(t, appHits.Load())
		})
	}
}

func TestJavDBAppAPI_ErrorDoesNotExposeToken(t *testing.T) {
	const secret = "do-not-expose-this-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	api, err := newJavDBAppAPI(secret, server.URL+"/api", nil)
	require.NoError(t, err)
	_, err = api.search(t.Context(), "SONE-065")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}

func TestJavDBAppAPI_DoesNotForwardTokenAcrossRedirect(t *testing.T) {
	var redirectedToken string
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedToken = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"success":1,"data":{"movies":[]}}`)
	}))
	t.Cleanup(redirectTarget.Close)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	t.Cleanup(server.Close)

	api, err := newJavDBAppAPI("test-token", server.URL+"/api", nil)
	require.NoError(t, err)
	_, err = api.search(t.Context(), "SONE-065")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 302")
	assert.Empty(t, redirectedToken)
}

func TestJavDBAppAPI_NumberMatchingKeepsSeparatorsSignificant(t *testing.T) {
	assert.True(t, equivalentMovieNumbers("HEYZO-0258", "HEYZO-258"))
	assert.False(t, equivalentMovieNumbers("041914_793", "041914-793"))

	api := &javDBAppAPI{}
	results, err := api.search(t.Context(), "")
	assert.ErrorIs(t, err, provider.ErrInvalidKeyword)
	assert.Empty(t, results)
}

func TestJavDBAppAPI_Live(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("JAVDB_TEST_AUTHORIZATION"))
	if token == "" {
		t.Skip("JAVDB_TEST_AUTHORIZATION is not set")
	}

	api, err := newJavDBAppAPI(token, defaultAppAPIBaseURL, nil)
	require.NoError(t, err)
	for _, number := range []string{"HEYZO-0258", "MKBD-S19", "041914_793"} {
		t.Run(number, func(t *testing.T) {
			info, err := api.movieInfoByNumber(t.Context(), number)
			require.NoError(t, err)
			require.True(t, info.IsValid())
			assert.NotEmpty(t, info.Number)
			assert.NotEmpty(t, info.Title)
			t.Logf("%s => %s | %s", number, info.Number, info.Title)
		})
	}
}
