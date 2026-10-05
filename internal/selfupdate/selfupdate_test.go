package selfupdate

import (
	"runtime"
	"strings"
	"testing"
)

func TestAssetMatchesThisPlatform(t *testing.T) {
	release := Release{Tag: "1.2.0"}
	release.Assets = append(release.Assets, struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	}{Name: "ffsync-1.2.0-linux-amd64", URL: "https://example.com/l", Size: 10},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{Name: "ffsync-1.2.0-windows-amd64.exe", URL: "https://example.com/w", Size: 20},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{Name: "ffsync-1.2.0-darwin-arm64", URL: "https://example.com/m", Size: 30})

	name, url, size, ok := release.Asset()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "windows/amd64", "darwin/arm64":
		if !ok {
			t.Fatalf("no asset matched %s", name)
		}
		if size == 0 || url == "" {
			t.Errorf("asset %s has no url or size", name)
		}
		if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
			t.Errorf("windows asset %q has no .exe suffix", name)
		}
	default:
		if ok {
			t.Errorf("matched %q on a platform with no build", name)
		}
	}
}

// A tag written as v1.2.0 names the same assets as 1.2.0.
func TestAssetIgnoresATagPrefix(t *testing.T) {
	plain := Release{Tag: "1.2.0"}
	prefixed := Release{Tag: "v1.2.0"}

	want, _, _, _ := plain.Asset()
	if got, _, _, _ := prefixed.Asset(); got != want {
		t.Errorf("v-prefixed tag looked for %q, want %q", got, want)
	}
}
