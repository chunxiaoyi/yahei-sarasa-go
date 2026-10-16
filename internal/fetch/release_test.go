package fetch

import "testing"

func TestUIZipAssetName(t *testing.T) {
	if got := UIZipAssetName("1.0.40", true); got != "SarasaUi-TTF-1.0.40.zip" {
		t.Fatal(got)
	}
	if got := UIZipAssetName("1.0.40", false); got != "SarasaUi-TTF-Unhinted-1.0.40.zip" {
		t.Fatal(got)
	}
}

func TestVersionFromTagURL(t *testing.T) {
	v, err := versionFromTagURL("https://github.com/be5invis/Sarasa-Gothic/releases/tag/v1.0.42")
	if err != nil || v != "1.0.42" {
		t.Fatalf("got %q %v", v, err)
	}
	if _, err := versionFromTagURL("https://github.com/be5invis/Sarasa-Gothic/releases"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"": "", "latest": "", "v1.0.42": "1.0.42", " 1.0.40 ": "1.0.40"} {
		got, err := NormalizeVersion(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q %v", in, got, err)
		}
	}
	if _, err := NormalizeVersion("1.0.x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeProxy(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7890":                     "http://127.0.0.1:7890",
		"http=127.0.0.1:1;https=127.0.0.1:2": "http://127.0.0.1:2",
		"socks=127.0.0.1:1080":               "socks5://127.0.0.1:1080",
		"http://proxy:8080":                  "http://proxy:8080",
		"":                                   "",
	}
	for in, want := range cases {
		if got := normalizeProxy(in); got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}

func TestApplyMirror(t *testing.T) {
	got, err := ApplyMirror("https://ghfast.top", "https://github.com/x")
	if err != nil || got != "https://ghfast.top/https://github.com/x" {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := ApplyMirror("", "u"); got != "u" {
		t.Fatal(got)
	}
	if _, err := ApplyMirror("ftp://x", "u"); err == nil {
		t.Fatal("expected error")
	}
}
