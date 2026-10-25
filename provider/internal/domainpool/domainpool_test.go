package domainpool

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolNormalizesDeduplicatesAndBoundsDomains(t *testing.T) {
	primary, err := url.Parse("https://javdb.com/")
	require.NoError(t, err)

	pool := New()
	require.NoError(t, pool.SetBackups([]string{
		"https://www.Backup-One.example/path?ignored=true",
		"backup-one.example",
		"http://backup-two.example:8080",
		"backup-three.example, backup-four.example",
		"",
	}))

	assert.Equal(t, []string{
		"https://javdb.com/",
		"https://www.backup-one.example/",
		"http://backup-two.example:8080/",
		"https://backup-three.example/",
		"https://backup-four.example/",
	}, pool.Domains(primary))

	err = pool.SetBackups([]string{
		"backup-one.example",
		"backup-two.example",
		"backup-three.example",
		"backup-four.example",
		"backup-five.example",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 5 total domains")
}

func TestPoolMatchesConfiguredHosts(t *testing.T) {
	primary, err := url.Parse("https://javdb.com/")
	require.NoError(t, err)

	pool := New()
	require.NoError(t, pool.SetBackups([]string{"backup.example"}))

	backup, err := url.Parse("https://WWW.BACKUP.EXAMPLE/v/example")
	require.NoError(t, err)
	assert.True(t, pool.Matches(primary, backup))

	primaryWithWww, err := url.Parse("https://www.javdb.com/v/example")
	require.NoError(t, err)
	assert.True(t, pool.Matches(primary, primaryWithWww))

	other, err := url.Parse("https://backup.example.attacker.test/v/example")
	require.NoError(t, err)
	assert.False(t, pool.Matches(primary, other))
}

func TestPoolSupportsDynamicUpdates(t *testing.T) {
	primary, err := url.Parse("https://missav.ws/")
	require.NoError(t, err)

	pool := New()
	require.NoError(t, pool.SetBackups([]string{"missav.ai"}))
	assert.Len(t, pool.Candidates(primary), 2)

	require.NoError(t, pool.SetBackups(nil))
	assert.Equal(t, []string{"https://missav.ws/"}, pool.Domains(primary))
}
