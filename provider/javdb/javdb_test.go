package javdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
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
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	jav := New()
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

func TestJavDB_EquivalentMovieNumbers(t *testing.T) {
	tests := []struct {
		name     string
		left     string
		right    string
		expected bool
	}{
		{name: "HEYZO leading zero", left: "HEYZO-0258", right: "HEYZO-258", expected: true},
		{name: "SERO leading zero", left: "SERO-0170", right: "SERO-170", expected: true},
		{name: "date separator", left: "052612_348", right: "052612-348", expected: true},
		{name: "trailing suffix", left: "MXNB-001S", right: "MXNB-001S", expected: true},
		{name: "compound number", left: "HEYDOUGA-4030-2133", right: "HEYDOUGA-4030-2133", expected: true},
		{name: "alphanumeric number", left: "MKBD-S94", right: "MKBD-S94", expected: true},
		{name: "suffix is significant", left: "MXNB-001S", right: "MXNB-001", expected: false},
		{name: "compound suffix is significant", left: "HEYDOUGA-4030-2133", right: "HEYDOUGA-4030", expected: false},
		{name: "date suffix is significant", left: "052612_348", right: "052612-349", expected: false},
		{name: "related number", left: "SONE-065", right: "SONE-0651", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, equivalentMovieNumbers(test.left, test.right))
		})
	}
}

func TestJavDB_SearchMovieFiltersTitleResults(t *testing.T) {
	const keyword = "IPZ-959 解禁！人生初 生中出しセックス 本物ザーメンを膣中に暴射！ 天海つばさ"
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, keyword, r.URL.Query().Get("q"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("match", "IPZ-959", keyword, "/match.jpg")+
			card("unrelated", "ABCD-123", "まったく関係のない別の作品", "/unrelated.jpg")+`</div>`)
	})

	results, err := jav.SearchMovie(keyword)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "IPZ-959", results[0].Number)
	assert.Equal(t, "match", results[0].ID)
}

func TestJavDB_FallsBackToConfiguredDomain(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupHits++
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

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL + "/ignored-path"}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "Backup title", info.Title)
	assert.Equal(t, backup.URL+"/v/Backup1", info.Homepage)
	assert.Equal(t, backup.URL+"/backup-cover.jpg", info.CoverURL)
	assert.Equal(t, []string{backup.URL + "/samples/1.jpg"}, []string(info.PreviewImages))
	assert.Zero(t, primaryHits)
	assert.Equal(t, 2, backupHits)

	id, err := jav.ParseMovieIDFromURL(backup.URL + "/v/Backup1?locale=zh")
	require.NoError(t, err)
	assert.Equal(t, "Backup1", id)
}

func TestJavDB_RetriesBackupBeforeRotation(t *testing.T) {
	var primaryHits, firstBackupHits, firstBackupSearchHits, secondBackupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(primary.Close)

	firstBackup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstBackupHits++
		switch r.URL.Path {
		case "/search":
			firstBackupSearchHits++
			if firstBackupSearchHits < maxDomainAttempts {
				fmt.Fprint(w, `<html><body>maintenance</body></html>`)
				return
			}
			fmt.Fprint(w, `<div class="movie-list">`+
				card("AbC123", "SONE-065", "Recovered title", "/cover.jpg")+`</div>`)
		case "/v/AbC123":
			fmt.Fprint(w, detailPage("SONE-065", "Recovered title", "/cover.jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(firstBackup.Close)

	secondBackup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondBackupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("Backup2", "SONE-065", "Unexpected fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(secondBackup.Close)

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{firstBackup.URL, secondBackup.URL}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "Recovered title", info.Title)
	assert.Zero(t, primaryHits)
	assert.Equal(t, maxDomainAttempts, firstBackupSearchHits)
	assert.Equal(t, maxDomainAttempts+1, firstBackupHits)
	assert.Zero(t, secondBackupHits)
}

func TestJavDB_SearchRetriesThreeTimesThenRotatesDomain(t *testing.T) {
	var primaryHits, firstBackupHits, secondBackupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(primary.Close)

	firstBackup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstBackupHits++
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	t.Cleanup(firstBackup.Close)

	secondBackup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondBackupHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("Backup1", "SONE-065", "Backup title", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(secondBackup.Close)

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{firstBackup.URL, secondBackup.URL}))

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Backup title", results[0].Title)
	assert.Zero(t, primaryHits)
	assert.Equal(t, maxDomainAttempts, firstBackupHits)
	assert.Equal(t, 1, secondBackupHits)
}

func TestJavDB_PrimaryOnlyRetriesThreeTimes(t *testing.T) {
	var hits int
	jav := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits < maxDomainAttempts {
			fmt.Fprint(w, `<html><body>maintenance</body></html>`)
			return
		}
		fmt.Fprint(w, `<div class="movie-list">`+
			card("Recovered1", "SONE-065", "Recovered title", "/cover.jpg")+`</div>`)
	})

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Recovered title", results[0].Title)
	assert.Equal(t, maxDomainAttempts, hits)
}

func TestJavDB_PrimaryOnlyCooldownSuppressesRequests(t *testing.T) {
	var hits int
	jav := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	})
	current := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	jav.now = func() time.Time { return current }

	results, err := jav.SearchMovie("SONE-065")
	require.ErrorIs(t, err, errDomainCooldown)
	assert.Empty(t, results)
	assert.Equal(t, maxDomainAttempts, hits)
	assert.Equal(t, current.Add(15*time.Minute), jav.domainCooldownUntil)

	results, err = jav.SearchMovie("SONE-065")
	require.ErrorIs(t, err, errDomainCooldown)
	assert.Empty(t, results)
	assert.Equal(t, maxDomainAttempts, hits)
}

func TestJavDB_FallsBackToPrimaryAfterBackupLoginRequired(t *testing.T) {
	var primaryHits, backupHits int
	var primaryCookies, backupCookies []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		primaryCookies = append(primaryCookies, r.Header.Get("Cookie"))
		if primaryHits < maxDomainAttempts {
			fmt.Fprint(w, `<html><body>maintenance</body></html>`)
			return
		}
		fmt.Fprint(w, detailPage("SONE-065", "Primary title", "/primary-cover.jpg"))
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupHits++
		backupCookies = append(backupCookies, r.Header.Get("Cookie"))
		fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body><form action="/login"><input type="password" name="password"></form></body></html>`)
	}))
	t.Cleanup(backup.Close)

	jav := New()
	primaryURL := strings.Replace(primary.URL, "127.0.0.1", "localhost", 1)
	jav.Scraper = scraper.NewDefaultScraper(Name, primaryURL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))
	_, err := jav.ConfigureSession("", "", "session=primary-only")
	require.NoError(t, err)

	info, err := jav.GetMovieInfoByID("wKg2O2")
	require.NoError(t, err)
	assert.Equal(t, "Primary title", info.Title)
	assert.Equal(t, primaryURL+"/primary-cover.jpg", info.CoverURL)
	assert.Equal(t, 1, backupHits)
	assert.Equal(t, maxDomainAttempts, primaryHits)
	require.Len(t, backupCookies, 1)
	assert.Empty(t, backupCookies[0])
	require.Len(t, primaryCookies, maxDomainAttempts)
	for _, cookie := range primaryCookies {
		assert.Contains(t, cookie, "session=primary-only")
	}
}

func TestJavDB_PrimaryNotFoundDoesNotStartCooldown(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<div class="movie-list"></div><div class="empty-message">沒有資料</div>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body><form action="/login"><input type="password" name="password"></form></body></html>`)
	}))
	t.Cleanup(backup.Close)

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	info, err := jav.GetMovieInfoByID("SONE-065")
	assert.Nil(t, info)
	require.ErrorIs(t, err, provider.ErrInfoNotFound)
	assert.Equal(t, 1, backupHits)
	assert.Equal(t, 1, primaryHits)
	assert.Zero(t, jav.domainFailureRounds)
	assert.True(t, jav.domainCooldownUntil.IsZero())
}

func TestJavDB_LoginDocumentRequiresPasswordField(t *testing.T) {
	parse := func(t *testing.T, body string) *goquery.Document {
		t.Helper()
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		require.NoError(t, err)
		return doc
	}

	t.Run("title and link only", func(t *testing.T) {
		doc := parse(t, `<html><head><title>登入 - JavDB</title></head><body><a href="/login">登录</a></body></html>`)
		assert.False(t, isLoginDocument(doc))
	})
	t.Run("password form", func(t *testing.T) {
		doc := parse(t, `<html><head><title>JavDB</title></head><body><form action="/login"><input type="password" name="password"></form></body></html>`)
		assert.True(t, isLoginDocument(doc))
	})
}

func TestJavDB_ConfigureDomainsIsAtomic(t *testing.T) {
	jav := New()
	require.NoError(t, jav.SetPrimaryDomain("https://primary.example"))
	require.NoError(t, jav.SetBackupDomains([]string{"https://backup.example"}))
	wantDomains := []string{"https://primary.example/", "https://backup.example/"}
	require.Equal(t, wantDomains, jav.Domains())

	badPrimary := "ftp://invalid-primary.example"
	validBackup := []string{"https://new-backup.example"}
	_, _, err := jav.ConfigureDomains(&badPrimary, &validBackup)
	require.Error(t, err)
	assert.Equal(t, "https://primary.example/", jav.URL().String())
	assert.Equal(t, wantDomains, jav.Domains())

	validPrimary := "https://new-primary.example"
	badBackup := []string{"ftp://invalid-backup.example"}
	_, _, err = jav.ConfigureDomains(&validPrimary, &badBackup)
	require.Error(t, err)
	assert.Equal(t, "https://primary.example/", jav.URL().String())
	assert.Equal(t, wantDomains, jav.Domains())
}

func TestJavDB_ConfigureSessionCookieAndClear(t *testing.T) {
	var cookies []string
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		cookies = append(cookies, r.Header.Get("Cookie"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("cookie", "SONE-065", "Cookie session", "/cover.jpg")+`</div>`)
	})

	status, err := jav.ConfigureSession("", "", "Cookie: session=abc; cf_clearance=token")
	require.NoError(t, err)
	assert.True(t, status.Configured)
	assert.True(t, status.Authenticated)
	assert.Equal(t, "cookie", status.Mode)

	_, err = jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, cookies, 1)
	assert.Contains(t, cookies[0], "session=abc")
	assert.Contains(t, cookies[0], "cf_clearance=token")

	status, err = jav.ConfigureSession("", "", "")
	require.NoError(t, err)
	assert.False(t, status.Configured)
	assert.Equal(t, "none", status.Mode)

	_, err = jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, cookies, 2)
	assert.Empty(t, cookies[1])
}

func TestJavDB_SessionCookiesOnlyApplyToPrimaryDomain(t *testing.T) {
	var primaryCookies, backupCookies []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCookies = append(primaryCookies, r.Header.Get("Cookie"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("primary", "SONE-065", "Primary title", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCookies = append(backupCookies, r.Header.Get("Cookie"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("backup", "SONE-065", "Backup title", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := New()
	primaryURL := strings.Replace(primary.URL, "127.0.0.1", "localhost", 1)
	jav.Scraper = scraper.NewDefaultScraper(Name, primaryURL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))
	_, err := jav.ConfigureSession("", "", "session=primary-only")
	require.NoError(t, err)

	_, err = jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, backupCookies, 1)
	assert.Empty(t, backupCookies[0])
	assert.Empty(t, primaryCookies)
}

func TestJavDB_SetPrimaryDomainClearsPreviousSessionCookies(t *testing.T) {
	var oldPrimaryCookies []string
	oldPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldPrimaryCookies = append(oldPrimaryCookies, r.Header.Get("Cookie"))
		fmt.Fprint(w, `<div class="movie-list">`+
			card("old", "SONE-065", "Old primary title", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(oldPrimary.Close)

	newPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected primary request", http.StatusInternalServerError)
	}))
	t.Cleanup(newPrimary.Close)

	jav := New()
	oldPrimaryURL := strings.Replace(oldPrimary.URL, "127.0.0.1", "localhost", 1)
	jav.Scraper = scraper.NewDefaultScraper(Name, oldPrimaryURL, Priority, language.Chinese)
	_, err := jav.ConfigureSession("", "", "session=old-primary")
	require.NoError(t, err)

	require.NoError(t, jav.SetPrimaryDomain(newPrimary.URL))
	require.NoError(t, jav.SetBackupDomains([]string{oldPrimary.URL}))

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, oldPrimaryCookies, 1)
	assert.Empty(t, oldPrimaryCookies[0])
}

func TestJavDB_ConfigureSessionWithPassword(t *testing.T) {
	var postHits int
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/login":
			fmt.Fprint(w, `<html><head><meta name="csrf-token" content="csrf-123"></head><body>
<form action="/auth/signin" method="post">
<input name="email"><input name="password"><input name="remember">
</form></body></html>`)
		case r.Method == http.MethodPost && r.URL.Path == "/auth/signin":
			postHits++
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "user@example.com", r.Form.Get("email"))
			assert.Equal(t, " secret-password ", r.Form.Get("password"))
			assert.Equal(t, "1", r.Form.Get("remember"))
			assert.Equal(t, "csrf-123", r.Header.Get("X-CSRF-Token"))
			assert.Contains(t, r.Header.Get("Referer"), "/login")
			assert.Contains(t, r.Header.Get("Origin"), "://")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "signed-in", Path: "/"})
			fmt.Fprint(w, `<html><head><title>JavDB</title></head><body>signed in</body></html>`)
		case r.URL.Path == "/search":
			assert.Contains(t, r.Header.Get("Cookie"), "session=signed-in")
			fmt.Fprint(w, `<div class="movie-list">`+
				card("signed-in", "SONE-065", "Password session", "/cover.jpg")+`</div>`)
		default:
			http.NotFound(w, r)
		}
	})

	status, err := jav.ConfigureSession("user@example.com", " secret-password ", "")
	require.NoError(t, err)
	assert.True(t, status.Configured)
	assert.True(t, status.Authenticated)
	assert.Equal(t, "password", status.Mode)
	assert.Equal(t, 1, postHits)

	_, err = jav.SearchMovie("SONE-065")
	require.NoError(t, err)
}

func TestJavDB_ConfigureSessionStopsAtCaptcha(t *testing.T) {
	var postHits int
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postHits++
		}
		fmt.Fprint(w, `<html><head><meta name="csrf-token" content="csrf-123"></head><body>
<form action="/auth/signin" method="post">
<input name="email"><input name="password"><input name="_rucaptcha">
<img src="/rucaptcha/123">
</form></body></html>`)
	})

	status, err := jav.ConfigureSession("user@example.com", "secret-password", "")
	require.NoError(t, err)
	assert.True(t, status.Configured)
	assert.False(t, status.Authenticated)
	assert.Equal(t, "password", status.Mode)
	assert.Contains(t, status.Message, "图片验证码")
	assert.Zero(t, postHits)
}

func TestJavDB_DoesNotFallbackAfterNormalEmptySearch(t *testing.T) {
	var primaryHits, backupHits int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		fmt.Fprint(w, `<div class="movie-list">`+
			card("Primary1", "SONE-065", "Unexpected primary fallback", "/cover.jpg")+`</div>`)
	}))
	t.Cleanup(primary.Close)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupHits++
		fmt.Fprint(w, `<div class="movie-list"></div><div class="empty-message">沒有資料</div>`)
	}))
	t.Cleanup(backup.Close)

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, primary.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetBackupDomains([]string{backup.URL}))

	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Zero(t, primaryHits)
	assert.Equal(t, 1, backupHits)
	assert.Zero(t, jav.domainFailureRounds)
	assert.True(t, jav.domainCooldownUntil.IsZero())
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

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, servers[0].URL, Priority, language.Chinese)
	backups := make([]string, 0, len(servers)-1)
	for _, server := range servers[1:] {
		backups = append(backups, server.URL)
	}
	require.NoError(t, jav.SetBackupDomains(backups))

	results, err := jav.SearchMovie("SONE-065")
	require.Error(t, err)
	assert.Empty(t, results)
	assert.Equal(t, []int{0, maxDomainAttempts, maxDomainAttempts, maxDomainAttempts, maxDomainAttempts}, hits)
	assert.ErrorIs(t, err, errDomainCooldown)
}

func TestJavDB_DomainCooldownEscalatesAndSuppressesRequests(t *testing.T) {
	const domainCount = 4
	var mu sync.Mutex
	hits := make([]int, domainCount)
	servers := make([]*httptest.Server, 0, domainCount)
	for i := 0; i < domainCount; i++ {
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

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, servers[0].URL, Priority, language.Chinese)
	backups := make([]string, 0, len(servers)-1)
	for _, server := range servers[1:] {
		backups = append(backups, server.URL)
	}
	require.NoError(t, jav.SetBackupDomains(backups))

	current := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	jav.now = func() time.Time { return current }
	durations := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 2 * time.Hour}
	for round, duration := range durations {
		if round > 0 {
			current = current.Add(durations[round-1])
		}

		results, err := jav.SearchMovie("SONE-065")
		require.ErrorIs(t, err, errDomainCooldown)
		assert.Empty(t, results)
		assert.Equal(t, current.Add(duration), jav.domainCooldownUntil)

		mu.Lock()
		expectedHits := (round + 1) * maxDomainAttempts
		assert.Equal(t, []int{0, expectedHits, expectedHits, expectedHits}, hits)
		mu.Unlock()

		_, err = jav.SearchMovie("SONE-065")
		require.ErrorIs(t, err, errDomainCooldown)
		mu.Lock()
		assert.Equal(t, []int{0, expectedHits, expectedHits, expectedHits}, hits,
			"requests during cooldown must not reach any domain")
		mu.Unlock()
	}
}

func TestJavDB_SuccessResetsDomainFailureRounds(t *testing.T) {
	const domainCount = 4
	var mu sync.Mutex
	fail := true
	hits := make([]int, domainCount)
	servers := make([]*httptest.Server, 0, domainCount)
	for i := 0; i < domainCount; i++ {
		index := i
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits[index]++
			shouldFail := fail
			mu.Unlock()
			if shouldFail {
				fmt.Fprint(w, `<html><body>maintenance</body></html>`)
				return
			}
			fmt.Fprint(w, `<div class="movie-list">`+
				card("Recovered1", "SONE-065", "Recovered title", "/cover.jpg")+`</div>`)
		}))
		servers = append(servers, server)
		t.Cleanup(server.Close)
	}

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, servers[0].URL, Priority, language.Chinese)
	backups := make([]string, 0, len(servers)-1)
	for _, server := range servers[1:] {
		backups = append(backups, server.URL)
	}
	require.NoError(t, jav.SetBackupDomains(backups))

	current := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	jav.now = func() time.Time { return current }
	_, err := jav.SearchMovie("SONE-065")
	require.ErrorIs(t, err, errDomainCooldown)
	assert.Equal(t, 1, jav.domainFailureRounds)

	current = current.Add(15 * time.Minute)
	mu.Lock()
	fail = false
	mu.Unlock()
	results, err := jav.SearchMovie("SONE-065")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Zero(t, jav.domainFailureRounds)
	assert.True(t, jav.domainCooldownUntil.IsZero())

	mu.Lock()
	fail = true
	mu.Unlock()
	_, err = jav.SearchMovie("SONE-065")
	require.ErrorIs(t, err, errDomainCooldown)
	assert.Equal(t, 1, jav.domainFailureRounds)
	assert.Equal(t, current.Add(15*time.Minute), jav.domainCooldownUntil)
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
				fmt.Fprint(w, `<html><head><title>登入 - JavDB</title></head><body><form action="/login"><input type="password" name="password"></form></body></html>`)
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

func TestJavDB_AcceptsNormalPageWithChallengePlatformScript(t *testing.T) {
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, `<div class="movie-list">`+
				card("AbC123", "SONE-065", "Example title", "/cover.jpg")+`</div>`)
		case "/v/AbC123":
			fmt.Fprint(w, detailPage("SONE-065", "Example title", "/cover.jpg")+
				`<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`)
		default:
			http.NotFound(w, r)
		}
	})

	info, err := jav.GetMovieInfoByID("SONE-065")
	require.NoError(t, err)
	assert.Equal(t, "SONE-065", info.Number)
	assert.Equal(t, "Example title", info.Title)
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
	jav := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		number := r.URL.Query().Get("q")
		fmt.Fprint(w, `<div class="movie-list">`+card("slug"+number, number, "Example", "/cover.jpg")+`</div>`)
	})
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

	jav := New()
	jav.Scraper = scraper.NewDefaultScraper(Name, target.URL, Priority, language.Chinese)
	require.NoError(t, jav.SetFlareSolverr(flare.URL+"/v1"))

	info, err := jav.GetMovieInfoByID("3119569")
	require.NoError(t, err)
	assert.Equal(t, "Solved title", info.Title)
	assert.Equal(t, target.URL+"/solved.jpg", info.CoverURL)
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
