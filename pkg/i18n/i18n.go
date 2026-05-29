// Package i18n provides language normalization utilities shared across services.
package i18n

import "strings"

// supportedLangs is the single source of truth for supported locale codes.
// Add a new entry here (and the matching frontend SUPPORTED_LANGS) to extend.
var supportedLangs = map[string]bool{
	"en": true,
	"zh": true,
}

const defaultLang = "en"

// Normalize maps a BCP-47 language tag to a supported locale code, falling
// back to "en" for anything unknown. E.g. "zh-CN" → "zh", "en-US" → "en".
func Normalize(lang string) string {
	lang = strings.TrimSpace(strings.ToLower(lang))
	if lang == "" {
		return defaultLang
	}
	// Exact match first.
	if supportedLangs[lang] {
		return lang
	}
	// Try the primary subtag (e.g. "zh" from "zh-CN").
	if idx := strings.IndexByte(lang, '-'); idx > 0 {
		primary := lang[:idx]
		if supportedLangs[primary] {
			return primary
		}
	}
	return defaultLang
}

// Supported reports whether lang (after normalisation) is a supported locale.
func Supported(lang string) bool {
	return supportedLangs[Normalize(lang)]
}

// Supported returns a slice of all supported locale codes.
func SupportedList() []string {
	out := make([]string, 0, len(supportedLangs))
	for k := range supportedLangs {
		out = append(out, k)
	}
	return out
}
