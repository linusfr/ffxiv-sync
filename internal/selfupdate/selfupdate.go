// Package selfupdate replaces the running binary with the newest release.
//
// Release assets carry their version in the name, so there is no stable URL to
// curl and updating by hand means reading the releases page first. On the
// machines that are not managed by Nix — which is most of them — that is the
// difference between updating and not bothering.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repository is where releases come from.
const Repository = "linusfr/ffxiv-sync"

// ErrManaged means the binary is not ours to replace.
var ErrManaged = fmt.Errorf("this copy is managed by its packager")

// Release is the part of GitHub's answer this needs.
type Release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Asset returns the download for this machine, and whether there is one.
func (r Release) Asset() (name, url string, size int64, ok bool) {
	want := fmt.Sprintf("ffsync-%s-%s-%s", strings.TrimPrefix(r.Tag, "v"), runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		want += ".exe"
	}

	for _, asset := range r.Assets {
		if asset.Name == want {
			return asset.Name, asset.URL, asset.Size, true
		}
	}

	return want, "", 0, false
}

// Latest asks GitHub what the newest release is.
func Latest(ctx context.Context) (Release, error) {
	var release Release

	address := "https://api.github.com/repos/" + Repository + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return release, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return release, fmt.Errorf("github answered %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return release, err
	}

	return release, nil
}

// Target is the binary that would be replaced, and an explanation when it
// cannot be. A copy in the Nix store is read-only by design: replacing it would
// be undone by the next rebuild and is not what the machine's owner asked for.
func Target() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}

	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(path, "/nix/store/") {
		return path, fmt.Errorf("%w: %s is in the Nix store — bump the version in your Nix config instead", ErrManaged, path)
	}

	// Writability of the directory, not the file: the replacement is a rename
	// into it, and on Windows the running binary itself cannot be opened for
	// writing at all.
	probe, err := os.CreateTemp(filepath.Dir(path), ".ffsync-update-")
	if err != nil {
		return path, fmt.Errorf("%w: %s is not writable (%v)", ErrManaged, filepath.Dir(path), err)
	}
	probe.Close()
	os.Remove(probe.Name())

	return path, nil
}

// Apply downloads the release and puts it in place of the running binary.
//
// The old binary is moved aside rather than deleted: Windows will not let a
// running executable be replaced, but it will let it be renamed, and a copy
// left behind is a better failure than a machine with no ffsync on it.
func Apply(ctx context.Context, release Release, target string) error {
	_, url, size, ok := release.Asset()
	if !ok {
		return fmt.Errorf("release %s has no build for %s/%s", release.Tag, runtime.GOOS, runtime.GOARCH)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", url, response.Status)
	}

	directory := filepath.Dir(target)
	temporary, err := os.CreateTemp(directory, ".ffsync-update-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())

	written, err := io.Copy(temporary, response.Body)
	if err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	// A truncated download is a binary that will not run, and it would be in
	// place before anyone found out.
	if size > 0 && written != size {
		return fmt.Errorf("downloaded %d bytes, expected %d", written, size)
	}
	if err := os.Chmod(temporary.Name(), 0o755); err != nil {
		return err
	}

	previous := target + ".old"
	os.Remove(previous)
	if err := os.Rename(target, previous); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), target); err != nil {
		// Put the working copy back rather than leaving the machine with none.
		os.Rename(previous, target)
		return err
	}

	// Unix replaces a running binary happily; Windows holds the old inode open
	// until the process exits, so the leftover is removed on the next run.
	os.Remove(previous)

	return nil
}
