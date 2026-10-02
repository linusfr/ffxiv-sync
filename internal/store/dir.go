package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/manifest"
)

// Dir is a store in a local folder. Replicating that folder with Syncthing or
// anything else turns it into a shared store with no server involved.
type Dir struct {
	Root string

	// Keep is how many past manifests to leave behind. Blobs they still refer
	// to survive; everything else is collected on commit.
	Keep int
}

const defaultKeep = 20

func (d *Dir) current() string   { return filepath.Join(d.Root, "current.json") }
func (d *Dir) manifests() string { return filepath.Join(d.Root, "manifests") }
func (d *Dir) blobs() string     { return filepath.Join(d.Root, "blobs") }

func (d *Dir) blobPath(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(d.blobs(), hash)
	}

	return filepath.Join(d.blobs(), hash[:2], hash)
}

// Current reads the newest manifest.
func (d *Dir) Current(context.Context) (*manifest.Manifest, error) {
	if err := d.checkConflicts(); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(d.current())
	if os.IsNotExist(err) {
		return manifest.New(), nil
	}
	if err != nil {
		return nil, err
	}

	m := manifest.New()
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("reading %s: %w", d.current(), err)
	}
	if m.Version > manifest.Version {
		return nil, fmt.Errorf("store is version %d, this ffsync understands %d", m.Version, manifest.Version)
	}

	return m, nil
}

// Blob reads one file's contents.
func (d *Dir) Blob(_ context.Context, hash string) ([]byte, error) {
	return os.ReadFile(d.blobPath(hash))
}

// Commit writes the blobs first, then swaps the manifest in with a rename, so a
// reader never sees a manifest pointing at a blob that is not there yet.
func (d *Dir) Commit(ctx context.Context, m *manifest.Manifest, blobs map[string][]byte) error {
	remote, err := d.Current(ctx)
	if err != nil {
		return err
	}
	if m.Generation != remote.Generation+1 {
		return ErrStale
	}

	for hash, data := range blobs {
		path := d.blobPath(hash)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeFile(path, data); err != nil {
			return err
		}
	}

	m.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(d.manifests(), 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(d.manifests(), fmt.Sprintf("%d.json", m.Generation)), data); err != nil {
		return err
	}
	if err := writeFile(d.current(), data); err != nil {
		return err
	}

	return d.collect()
}

// CheckConflicts refuses to work on a folder a sync tool is still arguing over,
// because picking a side automatically is how settings get lost.
func (d *Dir) checkConflicts() error {
	entries, err := os.ReadDir(d.Root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.Contains(name, ".sync-conflict-") || strings.HasSuffix(name, ".conflict") {
			return fmt.Errorf("%s has a sync conflict (%s); resolve it before syncing", d.Root, name)
		}
	}

	return nil
}

// Collect drops manifests older than Keep, and any blob nothing refers to.
func (d *Dir) collect() error {
	keep := d.Keep
	if keep <= 0 {
		keep = defaultKeep
	}

	entries, err := os.ReadDir(d.manifests())
	if err != nil {
		return err
	}

	live := map[string]bool{}
	var generations []int64
	kept := map[int64]bool{}

	for _, entry := range entries {
		var generation int64
		if _, err := fmt.Sscanf(entry.Name(), "%d.json", &generation); err != nil {
			continue
		}
		generations = append(generations, generation)
	}

	for _, generation := range generations {
		kept[generation] = true
	}
	if len(generations) > keep {
		newest := int64(0)
		for _, generation := range generations {
			if generation > newest {
				newest = generation
			}
		}
		for _, generation := range generations {
			if generation <= newest-int64(keep) {
				kept[generation] = false
				if err := os.Remove(filepath.Join(d.manifests(), fmt.Sprintf("%d.json", generation))); err != nil {
					return err
				}
			}
		}
	}

	for generation, alive := range kept {
		if !alive {
			continue
		}

		data, err := os.ReadFile(filepath.Join(d.manifests(), fmt.Sprintf("%d.json", generation)))
		if err != nil {
			return err
		}

		m := manifest.New()
		if err := json.Unmarshal(data, m); err != nil {
			return err
		}
		for _, entry := range m.Entries {
			// What the store holds, not what the file hashes to: with an
			// encrypted store those differ, and collecting by the wrong one
			// deletes every blob the manifest just referenced.
			live[entry.Stored()] = true
		}
	}

	return filepath.WalkDir(d.blobs(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if live[entry.Name()] {
			return nil
		}

		return os.Remove(path)
	})
}

// WriteFile replaces a file in one step, so a reader sees the old contents or
// the new ones and never half of each.
func writeFile(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".ffsync-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return err
	}

	return os.Rename(temp.Name(), path)
}
