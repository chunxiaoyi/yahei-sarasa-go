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
