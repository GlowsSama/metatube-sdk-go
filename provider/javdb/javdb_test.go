package javdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/testkit"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) *JavDB {
	t.Helper()
	return newTestProviderWithInterval(t, handler, 0)
}

func newTestProviderWithInterval(t *testing.T, handler http.HandlerFunc, interval time.Duration) *JavDB {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	jav := newJavDB(interval)
	jav.Scraper = scraper.NewDefaultScraper(Name, server.URL, Priority, language.Chinese)
	return jav
}

func card(slug, number, title, cover string) string {
	return fmt.Sprintf(`<div class="item"><a class="box" href="/v/%s" title="%s">
<div class="cover"><img src="%s"></div>
<div class="video-title"><strong>%s</strong> %s</div>
<div class="score"><span class="value">4.54分, 由100人評價</span></div>
<div class="meta">2022-10-30</div></a></div>`, slug, title, cover, number, title)
}

func detailPage(number, title, cover string) string {
	return fmt.Sprintf(`<html><head><title>%s - JavDB</title></head><body>
<h2 class="title is-4 current-title">%s</h2>
<img class="video-cover" src="%s">
<div class="movie-panel-info">
  <div class="panel-block"><strong>番號:</strong><span class="value"><span data-clipboard-text="%s">%s</span></span></div>
  <div class="panel-block"><strong>日期:</strong><span class="value">2026-08-01</span></div>
  <div class="panel-block"><strong>時長:</strong><span class="value">120 分鐘</span></div>
  <div class="panel-block"><strong>評分:</strong><span class="value">4.7分, 由321人評價</span></div>
  <div class="panel-block"><strong>導演:</strong><span class="value"><a>Director A</a></span></div>
  <div class="panel-block"><strong>片商:</strong><span class="value"><a>Maker A</a></span></div>
  <div class="panel-block"><strong>發行商:</strong><span class="value"><a>Label A</a></span></div>
  <div class="panel-block"><strong>系列:</strong><span class="value"><a>Series A</a></span></div>
  <div class="panel-block"><strong>類別:</strong><span class="value"><a>Genre A</a><a>Genre B</a></span></div>
  <div class="panel-block"><strong>演員:</strong><span class="value"><a>Actor A</a><a>Actor B</a></span></div>
</div>
<div class="preview-images"><a class="tile-item" href="/samples/1.jpg"><img></a></div>
</body></html>`, title, title, cover, number, number)
}

func TestJavDB_FC2SearchMetadata(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/search", r.URL.Path)
		assert.Equal(t, "FC2-3119569", r.URL.Query().Get("q"))
		assert.Equal(t, "all", r.URL.Query().Get("f"))
		assert.Equal(t, "zh", r.URL.Query().Get("locale"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("wrong1", "FC2-31195690", "Related item", "/wrong.jpg")+
			card("wrong2", "ABC-3119569", "Other publisher", "/wrong.jpg")+
			card("fc2slug", "FC2-3119569", "Example title", "/cover.jpg")+`</div>`)
	})
	info, err := jav.GetMovieInfoByID("FC2-PPV-3119569")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "3119569", info.ID)
	assert.Equal(t, "FC2-3119569", info.Number)
	assert.Equal(t, "Example title", info.Title)
	assert.Equal(t, Name, info.Provider)
	assert.Equal(t, jav.URL().String()+"/cover.jpg", info.CoverURL)
	assert.Equal(t, info.CoverURL, info.ThumbURL)
	assert.InDelta(t, 4.54, info.Score, 0.001)
	assert.Equal(t, "2022-10-30", time.Time(info.ReleaseDate).Format("2006-01-02"))
	assert.Empty(t, info.Actors)

	// Persisted numeric FC2 IDs and legacy search URLs remain supported.
	refetched, err := jav.GetMovieInfoByID(info.ID)
	require.NoError(t, err)
	assert.Equal(t, info, refetched)
	byURL, err := jav.GetMovieInfoByURL("https://javdb.com/search?q=FC2-PPV-3119569")
	require.NoError(t, err)
	assert.Equal(t, info, byURL)
}

func TestJavDB_SearchMovie(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/search", r.URL.Path)
		assert.Equal(t, "SONE-065", r.URL.Query().Get("q"))
		assert.Equal(t, "all", r.URL.Query().Get("f"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("AbC123", "SONE-065", "Example title", "/cover.jpg")+
			card("DeF456", "SONE-0651", "Related title", "/related.jpg")+`</div>`)
	})
	results, err := jav.SearchMovie("sone-065.mkv")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "AbC123", results[0].ID)
	assert.Equal(t, "SONE-065", results[0].Number)
	assert.Equal(t, "Example title", results[0].Title)
	assert.Equal(t, jav.URL().String()+"/v/AbC123", results[0].Homepage)
	assert.Equal(t, jav.URL().String()+"/cover.jpg", results[0].CoverURL)
	assert.True(t, results[0].IsValid())
}

func TestJavDB_SearchMovieFiltersRelatedNumbers(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "MUKD-502", r.URL.Query().Get("q"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("related1", "MUKD-552", "Related 552", "/related1.jpg")+
			card("related2", "MUKD-542", "Related 542", "/related2.jpg")+
			card("related3", "MUKD-532", "Related 532", "/related3.jpg")+`</div>`)
	})

	results, err := jav.SearchMovie("MUKD-502")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestJavDB_FallsBackToConfiguredDomain(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "http://"+r.Host+"/", r.Referer())
		switch r.URL.Path {
		case "/search":
			assert.Equal(t, "SONE-065", r.URL.Query().Get("q"))
			fmt.Fprint(w, `<div class="movie-list">`+
				card("Backup1", "SONE-065", "Backup title", "/backup-cover.jpg")+`</div>`)
		case "/v/Backup1":
			fmt.Fprint(w, detailPage("SONE-065", "Backup title", "/backup-cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL + "/ignored-path"}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "Backup title", info.Title)
	assert.Equal(t, backup.URL+"/v/Backup1", info.Homepage)
	assert.Equal(t, backup.URL+"/backup-cover.jpg", info.CoverURL)
	assert.Equal(t, []string{backup.URL + "/samples/1.jpg"}, []string(info.PreviewImages))

	id, err := jav.ParseMovieIDFromURL(backup.URL + "/v/Backup1?locale=zh")
	require.NoError(t, err)
	assert.Equal(t, "Backup1", id)
}

func TestJavDB_FallsBackAfterLoginRequired(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body><form action="/login"></form></body></html>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, `<div class="movie-list">`+
				card("Backup1", "SONE-065", "Backup title", "/backup-cover.jpg")+`</div>`)
		case "/v/Backup1":
			fmt.Fprint(w, detailPage("SONE-065", "Backup title", "/backup-cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "Backup title", info.Title)
	assert.Equal(t, backup.URL+"/v/Backup1", info.Homepage)
}

func TestJavDB_DoesNotFallbackAfterNormalEmptySearch(t *testing.T) {
	var backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<div class="movie-list"></div><div class="empty-message">沒有資料</div>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("Backup1", "SONE-065", "Unexpected fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Zero(t, backupHits)
}

func TestJavDB_StopsFallbackAfterForbidden(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("backup", "SONE-065", "Unexpected fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	_, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.ErrorIs(t, err, errRateLimited)
	assert.Equal(t, 3, primaryHits)
	assert.Zero(t, backupHits)
	assert.False(t, jav.rateLimitUntil.IsZero())

	_, err = jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.ErrorIs(t, err, errRateLimited)
	assert.Equal(t, 3, primaryHits, "cooldown should suppress the request after retries are exhausted")
	assert.Zero(t, backupHits)
}

func TestJavDB_RetryAfterStopsFallback(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("backup", "SONE-065", "Unexpected fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.retryWait = func(time.Duration) {}
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	_, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.ErrorIs(t, err, errRateLimited)
	assert.Equal(t, 3, primaryHits)
	assert.Zero(t, backupHits)
	assert.True(t, jav.rateLimitUntil.After(time.Now().Add(1500*time.Millisecond)))
}

func TestJavDB_StopsFallbackAfterCloudflareChallenge(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<html><title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/x"></script></html>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("backup", "SONE-065", "Unexpected fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	_, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.ErrorIs(t, err, errAntiBotChallenge)
	assert.Equal(t, 3, primaryHits)
	assert.Zero(t, backupHits)
	assert.False(t, jav.rateLimitUntil.IsZero())
}

func TestJavDB_RetriesTransientFailures(t *testing.T) {
	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		if primaryHits < 3 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `<div class="movie-list">`+
			card("SONE-065", "SONE-065", "Recovered title", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(primary.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Recovered title", results[0].Title)
	assert.Equal(t, 3, primaryHits)
	assert.True(t, jav.rateLimitUntil.IsZero(), "a later successful attempt must clear transient failures")
}

func TestJavDB_RetriesNetworkErrors(t *testing.T) {
	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		if primaryHits < 3 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server does not support connection hijacking")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack connection: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		fmt.Fprint(w, `<div class="movie-list">`+
			card("SONE-065", "SONE-065", "Recovered after network errors", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(primary.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Recovered after network errors", results[0].Title)
	assert.Equal(t, 3, primaryHits)
}

func TestJavDB_DoesNotRetryGeoRestriction(t *testing.T) {
	var primaryHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<h1>Due to copyright restrictions, access to this site is prohibited in the country where your internet is located.</h1>`)
	}))
	t.Cleanup(primary.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)

	_, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.ErrorIs(t, err, errGeoRestricted)
	assert.Equal(t, 1, primaryHits)
}

func TestJavDB_DomainFallbackIsBounded(t *testing.T) {
	var mu sync.Mutex
	hits := make([]int, 5)
	servers := make([]*httptest.Server, 0, 5)
	for i := range 5 {
		index := i
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits[index]++
			mu.Unlock()
			fmt.Fprint(w, `<html><body>maintenance</body></html>`)
		}))
		servers = append(servers, server)
		t.Cleanup(server.Close)
	}

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, servers[0].URL, Priority, language.Chinese)
	backups := make([]string, 0, len(servers)-1)
	for _, server := range servers[1:] {
		backups = append(backups, server.URL)
	}
	require.NoError(t, jav.SetBackupDomains(backups))

	results, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.Empty(t, results)
	assert.Equal(t, []int{1, 1, 1, 1, 1}, hits)
}

func TestJavDB_NumberLookupFetchesDetailPage(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			assert.Equal(t, "SONE-065", r.URL.Query().Get("q"))
			fmt.Fprint(w, `<div class="movie-list">`+
				card("Related1", "SONE-0651", "Related", "/related.jpg")+
				card("AbC123", "SONE-065", "Example title", "/card.jpg")+`</div>`)
		case "/v/AbC123":
			fmt.Fprint(w, detailPage("SONE-065", "Example title", "/cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	})

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	require.True(t, info.IsValid())
	assert.Equal(t, "AbC123", info.ID)
	assert.Equal(t, "SONE-065", info.Number)
	assert.Equal(t, "Example title", info.Title)
	assert.Equal(t, "Director A", info.Director)
	assert.Equal(t, "Maker A", info.Maker)
	assert.Equal(t, "Label A", info.Label)
	assert.Equal(t, "Series A", info.Series)
	assert.Equal(t, []string{"Actor A", "Actor B"}, []string(info.Actors))
	assert.Equal(t, []string{"Genre A", "Genre B"}, []string(info.Genres))
	assert.Equal(t, []string{jav.URL().String() + "/samples/1.jpg"}, []string(info.PreviewImages))
	assert.Equal(t, 120, info.Runtime)
	assert.InDelta(t, 4.7, info.Score, 0.001)
}

func TestJavDB_RequestIntervalAppliesToSearchDetailAndFallback(t *testing.T) {
	const interval = 40 * time.Millisecond

	type requestEvent struct {
		name string
		at   time.Time
	}
	events := make(chan requestEvent, 3)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		events <- requestEvent{name: "primary-search", at: time.Now()}
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			events <- requestEvent{name: "backup-search", at: time.Now()}
			fmt.Fprint(w, `<div class="movie-list">`+
				card("Backup1", "SONE-065", "Backup title", "/backup-cover.jpg")+`</div>`)
		case "/v/Backup1":
			events <- requestEvent{name: "backup-detail", at: time.Now()}
			fmt.Fprint(w, detailPage("SONE-065", "Backup title", "/backup-cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backup.Close)

	jav := newJavDB(interval)
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "Backup title", info.Title)

	received := []requestEvent{
		<-events,
		<-events,
		<-events,
	}
	assert.Equal(t, []string{"primary-search", "backup-search", "backup-detail"}, []string{
		received[0].name,
		received[1].name,
		received[2].name,
	})
	for i := 1; i < len(received); i++ {
		assert.GreaterOrEqual(t, received[i].at.Sub(received[i-1].at), interval-5*time.Millisecond)
	}
}

func TestJavDB_DetailSlugAndSimplifiedLabels(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v/wKg2O2", r.URL.Path)
		fmt.Fprint(w, `<h1 class="current-title">Slug title</h1>
<div class="video-cover"><img data-src="/cover.jpg"></div>
<nav class="movie-panel-info">
  <div><strong>番号：</strong><span class="value">HEYZO-3839</span></div>
  <div><strong>时长：</strong><span class="value">90 分钟</span></div>
  <div><strong>导演：</strong><span class="value">Director B</span></div>
  <div><strong>类别：</strong><span class="value"><a>Genre C</a></span></div>
  <div><strong>演员：</strong><span class="value"><a>Actor C</a></span></div>
</nav>
<div class="tile-images preview-images"><a href="/samples/2.jpg"></a></div>`)
	})

	info, err := jav.GetMovieInfoByID("wKg2O2")
	require.NoError(t, err)
	assert.Equal(t, "wKg2O2", info.ID)
	assert.Equal(t, "HEYZO-3839", info.Number)
	assert.Equal(t, "Director B", info.Director)
	assert.Equal(t, 90, info.Runtime)
	assert.Equal(t, []string{"Actor C"}, []string(info.Actors))
	assert.Equal(t, []string{"Genre C"}, []string(info.Genres))
	assert.Equal(t, []string{jav.URL().String() + "/samples/2.jpg"}, []string(info.PreviewImages))
}

func TestJavDB_FetchImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, baseURL, r.Referer())
		assert.NotEmpty(t, r.UserAgent())
		w.Header().Set("Content-Type", "image/jpeg")
		fmt.Fprint(w, "image bytes")
	}))
	t.Cleanup(server.Close)
	resp, err := New().Fetch(server.URL + "/cover.jpg")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	assert.Equal(t, "image bytes", string(body))
}

func TestJavDB_MissingOrIncompleteMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		call    func(*JavDB) (*model.MovieInfo, error)
		assert  func(*testing.T, error)
	}{
		{
			name: "empty search",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<div class="movie-list"></div><div class="empty-message">沒有資料</div>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("SONE-065") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, provider.ErrInfoNotFound)
			},
		},
		{
			name: "related only",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<div class="movie-list">`+card("related", "SONE-0651", "Related", "/cover.jpg")+`</div>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("SONE-065") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, provider.ErrInfoNotFound)
			},
		},
		{
			name: "login page",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body><form action="/login"></form></body></html>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, errLoginRequired)
			},
		},
		{
			name: "missing cover",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, detailPage("SONE-065", "Example", ""))
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, provider.ErrIncompleteMetadata)
			},
		},
		{
			name: "missing title",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<img class="video-cover" src="/cover.jpg"><div class="panel-block"><strong>番號:</strong><span class="value">SONE-065</span></div>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.Error(t, err)
				assert.False(t, errors.Is(err, provider.ErrInfoNotFound))
			},
		},
		{
			name: "geo restriction",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<h1>Due to copyright restrictions, access to this site is prohibited in the country where your internet is located.</h1>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, errGeoRestricted)
			},
		},
		{
			name: "anti-bot challenge",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<html><title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/x"></script></html>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, errAntiBotChallenge)
			},
		},
		{
			name: "anti-bot challenge with 403",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `<html><title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/x"></script></html>`)
			},
			call: func(j *JavDB) (*model.MovieInfo, error) { return j.GetMovieInfoByID("wKg2O2") },
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, errAntiBotChallenge)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jav := newTestProvider(t, tc.handler)
			info, err := tc.call(jav)
			assert.Nil(t, info)
			require.Error(t, err)
			tc.assert(t, err)
		})
	}
}

func TestJavDB_HTTPError(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	info, err := jav.GetMovieInfoByID("3119569")
	assert.Nil(t, info)
	require.Error(t, err)
	assert.False(t, errors.Is(err, provider.ErrInfoNotFound))
}

func TestJavDB_NormalizeMovieIDAndParseURL(t *testing.T) {
	jav := New()
	assert.Equal(t, 10*time.Second, jav.requestInterval)
	for _, id := range []string{"3119569", "FC2-3119569", "fc2-ppv-3119569", "FC2PPV3119569"} {
		assert.Equal(t, "3119569", jav.NormalizeMovieID(id))
	}
	assert.Equal(t, "SONE-065", jav.NormalizeMovieID("sone-065"))
	assert.Equal(t, "wKg2O2", jav.NormalizeMovieID("wKg2O2"))
	assert.Equal(t, "fc2slug", jav.NormalizeMovieID("fc2slug"))
	assert.Equal(t, "example", jav.NormalizeMovieID("example"))
	for _, id := range []string{"FC2-31195690x", "../example", "a/b", ""} {
		assert.Empty(t, jav.NormalizeMovieID(id))
		_, err := jav.GetMovieInfoByID(id)
		assert.ErrorIs(t, err, provider.ErrInvalidID)
	}

	for rawURL, want := range map[string]string{
		"https://javdb.com/search?q=FC2-PPV-3119569": "FC2-3119569",
		"https://javdb.com/search?q=SONE-065":        "SONE-065",
		"https://www.javdb.com/v/wKg2O2?locale=zh":   "wKg2O2",
		"https://JAVDB.com/v/fc2slug":                "fc2slug",
	} {
		id, err := jav.ParseMovieIDFromURL(rawURL)
		require.NoError(t, err)
		assert.Equal(t, want, id)
	}
	for _, rawURL := range []string{
		"https://javdb.com/search",
		"https://javdb.com/",
		"https://example.com/v/wKg2O2",
		"https://javdb.com.example.com/v/wKg2O2",
		"file:///v/wKg2O2",
	} {
		_, err := jav.ParseMovieIDFromURL(rawURL)
		assert.ErrorIs(t, err, provider.ErrInvalidURL)
	}
}

func TestJavDB_TitleAndLazyImageFallback(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<div class="movie-list"><div class="item"><a class="box" href="/v/example">
<div class="cover"><img data-src="/lazy.jpg"></div>
<div class="video-title"><strong>FC2-3119569</strong> Example &amp; title</div>
</a></div></div>`)
	})
	info, err := jav.GetMovieInfoByID("3119569")
	require.NoError(t, err)
	assert.Equal(t, "Example & title", info.Title)
	assert.Contains(t, info.CoverURL, "/lazy.jpg")
}

func TestJavDB_ConcurrentLookups(t *testing.T) {
	jav := newTestProviderWithInterval(t, func(w http.ResponseWriter, r *http.Request) {
		number := r.URL.Query().Get("q")
		fmt.Fprint(w, `<div class="movie-list">`+card("slug"+number, number, "Example", "/cover.jpg")+`</div>`)
	}, 20*time.Millisecond)
	var wg sync.WaitGroup
	for _, id := range []string{"3119569", "3108774"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := jav.GetMovieInfoByID(id)
			assert.NoError(t, err)
			if assert.NotNil(t, info) {
				assert.Equal(t, id, info.ID)
			}
		}()
	}
	wg.Wait()
}

func TestJavDB_UsesFlareSolverrForCloudflareChallenge(t *testing.T) {
	var targetHits, flareHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits++
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flareHits++
		var payload struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Contains(t, payload.URL, "/search?")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status":   http.StatusOK,
				"response": `<div class="movie-list">` + card("solved", "FC2-3119569", "Solved title", "/solved.jpg") + `</div>`,
			},
		}))
	}))
	t.Cleanup(flare.Close)

	jav := newJavDB(0)
	jav.Scraper = scraper.NewDefaultScraper(Name, target.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetFlareSolverr(flare.URL+"/v1"))

	info, err := jav.GetMovieInfoByID("3119569")
	require.NoError(t, err)
	assert.Equal(t, "Solved title", info.Title)
	assert.Equal(t, target.URL+"/solved.jpg", info.CoverURL)
	assert.Equal(t, 1, targetHits)
	assert.Equal(t, 1, flareHits)
	assert.True(t, jav.rateLimitUntil.IsZero(), "a solved challenge must not enter cooldown")
}

func TestJavDB_RetryAfterDelay(t *testing.T) {
	assert.Equal(t, 2*time.Second, retryAfterDelay("2"))
	assert.Zero(t, retryAfterDelay("not-a-delay"))

	delay := retryAfterDelay(time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat))
	assert.Greater(t, delay, 3*time.Second)
	assert.LessOrEqual(t, delay, 6*time.Second)
}

func TestJavDB_LiveFlareSolverr(t *testing.T) {
	endpoint := os.Getenv("METATUBE_TEST_FLARESOLVERR")
	if endpoint == "" {
		t.Skip("METATUBE_TEST_FLARESOLVERR is not set")
	}

	jav := New()
	jav.SetRequestTimeout(2 * time.Minute)
	require.NoError(t, jav.SetFlareSolverr(endpoint))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.IsValid())
}

func TestJavDB_GetMovieInfoByID(t *testing.T) {
	if os.Getenv("METATUBE_TEST_JAVDB") == "" {
		t.Skip("METATUBE_TEST_JAVDB is not set")
	}
	testkit.Test(t, New, []string{"SONE-065"})
}
