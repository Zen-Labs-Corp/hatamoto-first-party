package main

import "testing"

func TestNormalizeLocale(t *testing.T) {
	cases := []struct {
		in, want string
		bad      bool
	}{
		{"de", "de", false},
		{" DE_de ", "de-de", false},
		{"pt-BR", "pt-br", false},
		{"zh_Hant_TW", "zh-hant-tw", false},
		{"fil", "fil", false},
		{"", "", true},
		{"  ", "", true},
		{"d", "", true},
		{"german", "", true},
		{"xx-yy-zz-aa", "", true},
		{"de-", "", true},
		{"de/../x", "", true},
	}
	for _, c := range cases {
		got, err := normalizeLocale(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("normalizeLocale(%q) = %q, %v; want %q (bad=%v)", c.in, got, err, c.want, c.bad)
		}
	}
}

func TestDeeplTarget(t *testing.T) {
	cases := []struct{ in, want string }{
		{"de", "DE"}, {"de-at", "DE"}, {"de_CH", "DE"}, {"fr", "FR"}, {"ja", "JA"},
		{"en", "EN-GB"}, {"en-us", "EN-US"}, {"en-gb", "EN-GB"}, {"en-au", "EN-GB"}, {"en-ca", "EN-GB"},
		{"pt", "PT-PT"}, {"pt-pt", "PT-PT"}, {"pt-br", "PT-BR"},
		{"zh", "ZH-HANS"}, {"zh-cn", "ZH-HANS"}, {"zh-hans", "ZH-HANS"},
		{"zh-tw", "ZH-HANT"}, {"zh-hant", "ZH-HANT"}, {"zh-hant-tw", "ZH-HANT"},
		{"nb", "NB"}, {"no", "NB"}, {"uk", "UK"}, {"ar", "AR"},
	}
	for _, c := range cases {
		got, err := deeplTarget(c.in)
		if err != nil || got != c.want {
			t.Errorf("deeplTarget(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"tlh", "sw", "", "xx-yy-zz-aa"} {
		if got, err := deeplTarget(in); err == nil {
			t.Errorf("deeplTarget(%q) = %q, want an error", in, got)
		}
	}
	if _, err := deeplTarget("tlh"); err == nil || err.Error() != "DeepL does not support the locale tlh" {
		t.Errorf("unsupported error = %v", err)
	}
}

func TestDeeplSource(t *testing.T) {
	cases := []struct{ in, want string }{
		{"de", "DE"}, {"pt-br", "PT"}, {"en-gb", "EN"}, {"zh-hans", "ZH"}, {"zh-tw", "ZH"},
		{"no", "NB"}, {"tlh", ""}, {"", ""}, {"xx-yy-zz-aa", ""},
	}
	for _, c := range cases {
		if got := deeplSource(c.in); got != c.want {
			t.Errorf("deeplSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGoogleLang(t *testing.T) {
	cases := []struct{ in, want string }{
		{"de", "de"}, {"de-at", "de"}, {"en-us", "en"}, {"pt-br", "pt"}, {"pt", "pt"},
		{"zh", "zh-CN"}, {"zh-cn", "zh-CN"}, {"zh-hans", "zh-CN"},
		{"zh-tw", "zh-TW"}, {"zh-hant", "zh-TW"}, {"nb", "no"}, {"no", "no"},
		{"he", "he"}, {"sw", "sw"}, {"fil", "fil"}, {"ES_mx", "es"},
	}
	for _, c := range cases {
		got, err := googleLang(c.in)
		if err != nil || got != c.want {
			t.Errorf("googleLang(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"tlh", "", "xx-yy-zz-aa"} {
		if got, err := googleLang(in); err == nil {
			t.Errorf("googleLang(%q) = %q, want an error", in, got)
		}
	}
	if _, err := googleLang("tlh"); err == nil || err.Error() != "Google Translation does not support the locale tlh" {
		t.Errorf("unsupported error = %v", err)
	}
}

func TestDeeplFormalitySupported(t *testing.T) {
	for _, tg := range []string{"DE", "ES", "FR", "IT", "JA", "NL", "PL", "PT-BR", "PT-PT", "RU"} {
		if !deeplFormalitySupported(tg) {
			t.Errorf("%s should support formality", tg)
		}
	}
	for _, tg := range []string{"EN-GB", "EN-US", "ZH-HANS", "NB", "TR", "UK", "de", ""} {
		if deeplFormalitySupported(tg) {
			t.Errorf("%s should not support formality", tg)
		}
	}
}
