package javnumber

import (
	"path"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/metatube-community/metatube-sdk-go/common/number"
)

var (
	labelNumberRe     = regexp.MustCompile(`(?i)^([a-z][a-z0-9]*?)[-_\s]?(\d+)$`)
	uncensoredNumber  = regexp.MustCompile(`^\d{4,8}[-_]\d{2,5}$`)
	inlineNumberRe    = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]{0,20})[-_\s]?(\d{2,8})\b`)
	trailingVariantRe = regexp.MustCompile(`(?i)(?:-(?:uncensored(?:-leak)?|leak|english-subtitle|chinese-subtitle|japanese-subtitle|subtitle))+$`)
)

// Normalize converts a standalone movie number into the common CODE-123 form.
func Normalize(raw string) string {
	raw = CleanText(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimSuffix(raw, path.Ext(raw))
	raw = trailingVariantRe.ReplaceAllString(raw, "")
	raw = number.Trim(raw)
	if match := labelNumberRe.FindStringSubmatch(raw); len(match) == 3 {
		return strings.ToUpper(match[1]) + "-" + match[2]
	}
	if uncensoredNumber.MatchString(raw) {
		return strings.ToUpper(strings.ReplaceAll(raw, "_", "-"))
	}
	return ""
}

// Find extracts the first common movie number from free-form text.
func Find(raw string) string {
	raw = CleanText(raw)
	if normalized := Normalize(raw); normalized != "" {
		return normalized
	}
	match := inlineNumberRe.FindStringSubmatch(raw)
	if len(match) != 3 {
		return ""
	}
	return strings.ToUpper(match[1]) + "-" + match[2]
}

// CleanTitle removes repeated movie numbers from the beginning of a title.
func CleanTitle(title, code string) string {
	title = CleanText(title)
	code = CleanText(code)
	if title == "" || code == "" {
		return title
	}

	cleaned := title
	for len(cleaned) >= len(code) && strings.EqualFold(cleaned[:len(code)], code) {
		rest := cleaned[len(code):]
		trimmed := strings.TrimLeftFunc(rest, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("-:：|・—–", r)
		})
		if trimmed == rest {
			break
		}
		cleaned = trimmed
	}
	if cleaned == "" {
		return title
	}
	return cleaned
}

// CleanText folds full-width ASCII and collapses whitespace.
func CleanText(value string) string {
	value = width.Fold.String(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}
