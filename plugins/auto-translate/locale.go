package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The code tables below were taken from the providers' public language lists
// (DeepL "Supported languages", Google Cloud Translation "Language support").

var localeShape = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8}){0,2}$`)

// normalizeLocale lowercases, maps '_' to '-' and trims. An empty or oddly
// shaped locale is an error.
func normalizeLocale(s string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, "_", "-")))
	if !localeShape.MatchString(n) {
		return "", fmt.Errorf("%q is not a valid locale (use a tag such as de or pt-br)", strings.TrimSpace(s))
	}
	return n, nil
}

// localeParts splits a normalized locale into base language, script (4
// letters) and region (2 letters or 3 digits).
func localeParts(locale string) (base, script, region string) {
	parts := strings.Split(locale, "-")
	base = parts[0]
	for _, p := range parts[1:] {
		switch {
		case len(p) == 4 && p[0] >= 'a' && p[0] <= 'z':
			script = p
		case len(p) == 2 || len(p) == 3:
			region = p
		}
	}
	return base, script, region
}

// deeplBases are the DeepL languages that take the upper-cased base language
// as their code. EN, PT, ZH and NB have their own rules.
var deeplBases = map[string]bool{
	"ar": true, "bg": true, "cs": true, "da": true, "de": true, "el": true,
	"es": true, "et": true, "fi": true, "fr": true, "hu": true, "id": true,
	"it": true, "ja": true, "ko": true, "lt": true, "lv": true, "nl": true,
	"pl": true, "ro": true, "ru": true, "sk": true, "sl": true, "sv": true,
	"tr": true, "uk": true,
}

// deeplTarget maps a site locale to a DeepL target_lang code.
func deeplTarget(locale string) (string, error) {
	n, err := normalizeLocale(locale)
	if err != nil {
		return "", err
	}
	base, script, region := localeParts(n)
	switch base {
	case "en":
		if region == "us" {
			return "EN-US", nil
		}
		return "EN-GB", nil
	case "pt":
		if region == "br" {
			return "PT-BR", nil
		}
		return "PT-PT", nil
	case "zh":
		if script == "hant" || region == "tw" || region == "hk" || region == "mo" {
			return "ZH-HANT", nil
		}
		return "ZH-HANS", nil
	case "nb", "no":
		return "NB", nil
	}
	if deeplBases[base] {
		return strings.ToUpper(base), nil
	}
	return "", fmt.Errorf("DeepL does not support the locale %s", n)
}

// deeplSource maps a site locale to a DeepL source_lang code (the base
// language, upper case). It returns "" when the locale is unknown, so the
// caller omits source_lang and lets DeepL detect it.
func deeplSource(locale string) string {
	n, err := normalizeLocale(locale)
	if err != nil {
		return ""
	}
	base, _, _ := localeParts(n)
	switch base {
	case "en", "pt", "zh":
		return strings.ToUpper(base)
	case "nb", "no":
		return "NB"
	}
	if deeplBases[base] {
		return strings.ToUpper(base)
	}
	return ""
}

// deeplFormalityTargets are the DeepL targets that accept the formality
// parameter.
var deeplFormalityTargets = map[string]bool{
	"DE": true, "ES": true, "FR": true, "IT": true, "JA": true, "NL": true,
	"PL": true, "PT-BR": true, "PT-PT": true, "RU": true,
}

func deeplFormalitySupported(target string) bool { return deeplFormalityTargets[target] }

// googleBases are the language codes Google Cloud Translation lists, without
// the Chinese variants (zh-CN, zh-TW), which googleLang maps itself.
var googleBases = map[string]bool{
	"af": true, "ak": true, "am": true, "ar": true, "as": true, "ay": true,
	"az": true, "be": true, "bg": true, "bho": true, "bm": true, "bn": true,
	"bs": true, "ca": true, "ceb": true, "ckb": true, "co": true, "cs": true,
	"cy": true, "da": true, "de": true, "doi": true, "dv": true, "ee": true,
	"el": true, "en": true, "eo": true, "es": true, "et": true, "eu": true,
	"fa": true, "fi": true, "fil": true, "fr": true, "fy": true, "ga": true,
	"gd": true, "gl": true, "gn": true, "gom": true, "gu": true, "ha": true,
	"haw": true, "he": true, "hi": true, "hmn": true, "hr": true, "ht": true,
	"hu": true, "hy": true, "id": true, "ig": true, "ilo": true, "is": true,
	"it": true, "ja": true, "jv": true, "ka": true, "kk": true, "km": true,
	"kn": true, "ko": true, "kri": true, "ku": true, "ky": true, "la": true,
	"lb": true, "lg": true, "ln": true, "lo": true, "lt": true, "lus": true,
	"lv": true, "mai": true, "mg": true, "mi": true, "mk": true, "ml": true,
	"mn": true, "mr": true, "ms": true, "mt": true, "my": true, "ne": true,
	"nl": true, "no": true, "nso": true, "ny": true, "om": true, "or": true,
	"pa": true, "pl": true, "ps": true, "pt": true, "qu": true, "ro": true,
	"ru": true, "rw": true, "sa": true, "sd": true, "si": true, "sk": true,
	"sl": true, "sm": true, "sn": true, "so": true, "sq": true, "sr": true,
	"st": true, "su": true, "sv": true, "sw": true, "ta": true, "te": true,
	"tg": true, "th": true, "ti": true, "tk": true, "tl": true, "tr": true,
	"ts": true, "tt": true, "ug": true, "uk": true, "ur": true, "uz": true,
	"vi": true, "xh": true, "yi": true, "yo": true, "zu": true,
}

// googleLang maps a site locale to a Google Cloud Translation language code,
// used for both the target and the source.
func googleLang(locale string) (string, error) {
	n, err := normalizeLocale(locale)
	if err != nil {
		return "", err
	}
	base, script, region := localeParts(n)
	switch base {
	case "zh":
		if script == "hant" || region == "tw" || region == "hk" || region == "mo" {
			return "zh-TW", nil
		}
		return "zh-CN", nil
	case "nb":
		return "no", nil
	}
	if googleBases[base] {
		return base, nil
	}
	return "", fmt.Errorf("Google Translation does not support the locale %s", n)
}
