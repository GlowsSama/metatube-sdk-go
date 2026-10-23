package javnumber

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{
		"IPZ950":                   "IPZ-950",
		"IPZ 950":                  "IPZ-950",
		"SNOS_377":                 "SNOS-377",
		"IPZ-950-english-subtitle": "IPZ-950",
		"not-a-number":             "",
	} {
		assert.Equal(t, want, Normalize(input), input)
	}
}

func TestFind(t *testing.T) {
	assert.Equal(t, "IPZ-950", Find("IPZ-950 アタッカーズ全面監修 夫の目の前で犯されて"))
	assert.Equal(t, "SNOS-377", Find("SNOS-377 新作"))
	assert.Empty(t, Find("一般タイトル"))
}

func TestCleanTitle(t *testing.T) {
	for input, want := range map[string]string{
		"IPZ-950 IPZ-950 アタッカーズ全面監修": "アタッカーズ全面監修",
		"IPZ-950 - IPZ-950 - 新作":     "新作",
		"IPZ-950 新作":                 "新作",
		"IPZZ-950 新作":                "IPZZ-950 新作",
	} {
		assert.Equal(t, want, CleanTitle(input, "IPZ-950"), input)
	}
}
