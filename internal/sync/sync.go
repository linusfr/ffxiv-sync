// Package sync compares what is on this machine with what is in the store and
// moves files in whichever direction was asked for.
package sync

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/cfg"
	"github.com/linusfr/ffxiv-sync/internal/crypt"
	"github.com/linusfr/ffxiv-sync/internal/dalamud"
	"github.com/linusfr/ffxiv-sync/internal/layout"
	"github.com/linusfr/ffxiv-sync/internal/manifest"
	"github.com/linusfr/ffxiv-sync/internal/policy"
	"github.com/linusfr/ffxiv-sync/internal/store"
)

// DefaultMaxFileBytes keeps plugin caches out of the store. Settings files are
// kilobytes; anything much larger is something a plugin can rebuild.
const DefaultMaxFileBytes = 1 << 20

// Options is how one run behaves.
type Options struct {
	Roots   layout.Roots
	Profile string
	Device  string

	// Force takes this side's answer in a conflict instead of reporting it.
	Force bool

	// DryRun reports what would happen and touches nothing.
	DryRun bool

	// Cfg decides how far each FFXIV.cfg section travels. The zero value keeps
	// graphics, resolution and input hardware on the machine that wrote them.
	Cfg cfg.Policy

	// Plugins overrides a plugin's scope by its name, for the ones whose
	// settings are about the machine rather than the player.
	Plugins map[string]cfg.Scope

	// MaxFileBytes drops anything larger, which is how caches stay out.
	MaxFileBytes int64

	// Passphrase encrypts blobs before they leave. Plugin settings hold API
	// tokens, and a store is someone else's disk.
	Passphrase string
}

func (o Options) maxBytes() int64 {
	if o.MaxFileBytes > 0 {
		return o.MaxFileBytes
	}

	return DefaultMaxFileBytes
}

// File is one local file as the store would hold it: the contents are already
// what would be uploaded, so FFXIV.cfg has lost its machine-specific lines and
// dalamudConfig.json is down to its repository list.
type File struct {
	Logical string
	Key     string
	Action  policy.Action
	Data    []byte
	Hash    string
	ModTime time.Time
}

// Change is one line of a report.
type Change struct {
	Logical string
	Note    string
}

// Report is what a run did, or would have done.
type Report struct {
	Generation int64
	Changed    []Change
	Conflicts  []Change
	Skipped    []Change
	Unchanged  int
}

// Lines renders a report for the terminal.
func (r Report) Lines() []string {
	var out []string
	for _, change := range r.Changed {
		out = append(out, fmt.Sprintf("  %-52s %s", change.Logical, change.Note))
	}
	for _, change := range r.Conflicts {
		out = append(out, fmt.Sprintf("  %-52s CONFLICT: %s", change.Logical, change.Note))
	}

	sort.Strings(out)
	return out
}

// root returns the directory a logical path lives under, and whether this
// machine has it at all: Dalamud may not be installed.
func (o Options) root(logical string) (string, bool) {
	namespace, rest, _ := strings.Cut(logical, "/")
	switch namespace {
	case policy.Game:
		return filepath.Join(o.Roots.GameConfig, filepath.FromSlash(rest)), o.Roots.GameConfig != ""
	case policy.Plugins:
		return filepath.Join(o.Roots.PluginConfigs, filepath.FromSlash(rest)), o.Roots.PluginConfigs != ""
	case policy.Dalamud:
		return o.Roots.DalamudConfig, o.Roots.DalamudConfig != ""
	default:
		return "", false
	}
}

// pluginScope reports how far one plugin's settings travel. Most are the
// player's and shared; a plugin whose settings describe the screen can be
// pinned to a profile, or kept at home.
func (o Options) pluginScope(logical string) cfg.Scope {
	rest := strings.TrimPrefix(logical, policy.Plugins+"/")
	name, _, _ := strings.Cut(rest, "/")
	name = strings.TrimSuffix(name, ".json")

	if scope, ok := o.Plugins[name]; ok {
		return scope
	}

	return cfg.Shared
}

// Scan reads every file the policy has an opinion about.
func Scan(opts Options) ([]File, []Change, error) {
	var files []File
	var skipped []Change

	collect := func(logical string, data []byte, modTime time.Time, action policy.Action, key string) {
		files = append(files, File{
			Logical: logical,
			Key:     key,
			Action:  action,
			Data:    data,
			Hash:    manifest.Hash(data),
			ModTime: modTime,
		})
	}

	walk := func(root, namespace string) error {
		if root == "" {
			return nil
		}

		return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			if relative == "." {
				return nil
			}
			logical := path.Join(namespace, filepath.ToSlash(relative))

			if entry.IsDir() {
				// Backups, screenshots and plugin caches: large, and never
				// wanted, so the walk turns around at the top of them.
				if policy.SkipTree(logical) {
					return filepath.SkipDir
				}
				return nil
			}

			action := policy.For(logical)
			if action == policy.Skip {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > opts.maxBytes() {
				skipped = append(skipped, Change{
					Logical: logical,
					Note:    fmt.Sprintf("%.1f MiB, over the size cap", float64(info.Size())/(1<<20)),
				})
				return nil
			}

			scope := cfg.Shared
			if namespace == policy.Plugins {
				scope = opts.pluginScope(logical)
				if scope == cfg.Local {
					return nil
				}
				if scope == cfg.Profiled {
					action = policy.Profile
				}
			}

			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}

			switch action {
			case policy.Merge:
				// One text file, two destinations: what every machine agrees
				// on, and what only machines of this shape do.
				parsed := cfg.Parse(data)
				for _, scope := range []cfg.Scope{cfg.Shared, cfg.Profiled} {
					if !parsed.Travels(opts.Cfg, scope) {
						continue
					}

					where := policy.Sync
					if scope == cfg.Profiled {
						where = policy.Profile
					}
					part := parsed.Scoped(opts.Cfg, scope).Bytes()
					collect(logical, part, info.ModTime().UTC(), action, manifest.Key(logical, where, opts.Profile))
				}

			case policy.Repos:
				repos, err := dalamud.Repos(data)
				if err != nil {
					return err
				}
				if repos == nil {
					return nil
				}
				collect(logical, repos, info.ModTime().UTC(), action,
					manifest.Key(logical, policy.Sync, opts.Profile))

			default:
				collect(logical, data, info.ModTime().UTC(), action,
					manifest.Key(logical, action, opts.Profile))
			}

			return nil
		})
	}

	if err := walk(opts.Roots.GameConfig, policy.Game); err != nil {
		return nil, skipped, err
	}
	if err := walk(opts.Roots.PluginConfigs, policy.Plugins); err != nil && !os.IsNotExist(err) {
		return nil, skipped, err
	}

	if opts.Roots.DalamudConfig != "" {
		info, err := os.Stat(opts.Roots.DalamudConfig)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return nil, skipped, err
		default:
			data, err := os.ReadFile(opts.Roots.DalamudConfig)
			if err != nil {
				return nil, skipped, err
			}
			repos, err := dalamud.Repos(data)
			if err != nil {
				return nil, skipped, err
			}
			if repos != nil {
				logical := policy.Dalamud + "/dalamudConfig.json"
				collect(logical, repos, info.ModTime().UTC(), policy.Repos,
					manifest.Key(logical, policy.Sync, opts.Profile))
			}
		}
	}

	return files, skipped, nil
}

// Push sends local changes to the store.
func Push(ctx context.Context, s store.Store, opts Options) (Report, error) {
	report := Report{}

	files, skipped, err := Scan(opts)
	if err != nil {
		return report, err
	}
	report.Skipped = skipped

	remote, err := s.Current(ctx)
	if err != nil {
		return report, err
	}

	next := manifest.New()
	next.Generation = remote.Generation + 1
	next.Device = opts.Device
	for key, entry := range remote.Entries {
		next.Entries[key] = entry
	}

	var sealer *crypt.Sealer
	if opts.Passphrase != "" {
		if sealer, err = crypt.NewSealer(opts.Passphrase); err != nil {
			return report, err
		}
	}

	blobs := map[string][]byte{}
	for _, file := range files {
		existing, known := remote.Entries[file.Key]
		switch {
		case known && existing.Hash == file.Hash:
			report.Unchanged++
			continue

		// Someone else wrote this after the local copy was last touched, so
		// pushing would throw their change away.
		case known && !opts.Force && existing.ModTime.After(file.ModTime):
			report.Conflicts = append(report.Conflicts, Change{
				Logical: file.Logical,
				Note: fmt.Sprintf("store has a newer copy from %s (%s)",
					existing.Device, existing.ModTime.Local().Format(time.RFC822)),
			})
			continue
		}

		note := "new"
		if known {
			note = "updated"
		}
		report.Changed = append(report.Changed, Change{Logical: file.Logical, Note: note})

		stored := file.Data
		if sealer != nil {
			if stored, err = sealer.Seal(file.Data); err != nil {
				return report, err
			}
		}

		blob := manifest.Hash(stored)
		blobs[blob] = stored
		next.Entries[file.Key] = manifest.Entry{
			Hash:       file.Hash,
			Blob:       blob,
			Encrypted:  opts.Passphrase != "",
			Size:       int64(len(file.Data)),
			ModTime:    file.ModTime,
			Device:     opts.Device,
			Generation: next.Generation,
		}
	}

	report.Generation = next.Generation
	if len(report.Changed) == 0 || opts.DryRun {
		report.Generation = remote.Generation
		return report, nil
	}

	if err := s.Commit(ctx, next, blobs); err != nil {
		return report, err
	}

	return report, nil
}

// Pull writes the store's copy over this machine's, for the files that belong
// to it: shared ones and its own profile's.
func Pull(ctx context.Context, s store.Store, opts Options) (Report, error) {
	report := Report{}

	remote, err := s.Current(ctx)
	if err != nil {
		return report, err
	}
	report.Generation = remote.Generation

	files, skipped, err := Scan(opts)
	if err != nil {
		return report, err
	}
	report.Skipped = skipped

	local := map[string]File{}
	for _, file := range files {
		local[file.Key] = file
	}

	keys := make([]string, 0, len(remote.Entries))
	for key := range remote.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// FFXIV.cfg arrives in two parts, and only the first of them should push
	// the original aside.
	backed := map[string]bool{}
	opener := crypt.NewOpener(opts.Passphrase)

	for _, key := range keys {
		entry := remote.Entries[key]

		logical, mine := manifest.CutShared(key)
		if !mine {
			logical, mine = manifest.CutProfile(key, opts.Profile)
		}
		if !mine {
			continue
		}

		if _, ok := opts.root(logical); !ok {
			report.Skipped = append(report.Skipped, Change{
				Logical: logical,
				Note:    "nowhere to put it on this machine",
			})
			continue
		}
		if strings.HasPrefix(logical, policy.Plugins+"/") && opts.pluginScope(logical) == cfg.Local {
			report.Skipped = append(report.Skipped, Change{Logical: logical, Note: "kept local"})
			continue
		}

		have, exists := local[key]
		if exists && have.Hash == entry.Hash {
			report.Unchanged++
			continue
		}

		// This machine changed the file after the store's copy was written, so
		// writing would throw the local change away.
		if exists && !opts.Force && have.ModTime.After(entry.ModTime) {
			report.Conflicts = append(report.Conflicts, Change{
				Logical: logical,
				Note:    "changed here after the store's copy; push, or pull --force",
			})
			continue
		}

		note := "written"
		if !exists {
			note = "new"
		}
		report.Changed = append(report.Changed, Change{Logical: logical, Note: note})
		if opts.DryRun {
			continue
		}

		stored, err := s.Blob(ctx, entry.Stored())
		if err != nil {
			return report, fmt.Errorf("%s: %w", logical, err)
		}

		data, err := opener.Open(stored)
		if err != nil {
			return report, fmt.Errorf("%s: %w", logical, err)
		}
		if manifest.Hash(data) != entry.Hash {
			return report, fmt.Errorf("%s: the store's copy does not match its hash", logical)
		}

		if err := write(opts, logical, data, backed); err != nil {
			return report, fmt.Errorf("%s: %w", logical, err)
		}
		backed[logical] = true
	}

	return report, nil
}

// Write puts one file on disk, keeping a copy of what it replaced. FFXIV.cfg
// and dalamudConfig.json are merged rather than replaced, so this machine keeps
// its own resolution and its own plugin bookkeeping.
func write(opts Options, logical string, data []byte, backed map[string]bool) error {
	target, ok := opts.root(logical)
	if !ok {
		return fmt.Errorf("nowhere to put it on this machine")
	}

	current, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	switch policy.For(logical) {
	case policy.Merge:
		data = cfg.Merge(cfg.Parse(current), cfg.Parse(data), opts.Cfg).Bytes()

	case policy.Repos:
		// Nothing to merge into means Dalamud has never run here; writing a
		// config that is only a repository list would lose it the rest.
		if len(current) == 0 {
			return nil
		}
		if data, err = dalamud.Merge(current, data); err != nil {
			return err
		}
	}

	if len(current) > 0 && !backed[logical] {
		if err := os.WriteFile(target+".ffsync-bak", current, 0o644); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	return os.WriteFile(target, data, 0o644)
}
