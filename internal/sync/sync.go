// Package sync compares what is on this machine with what is in the store and
// moves files in whichever direction was asked for.
package sync

import (
	"context"
	"encoding/json"
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
	Scope   string
	Action  policy.Action
	Data    []byte
	Hash    string
	ModTime time.Time
}

// Change is one line of a report.
type Change struct {
	Logical string

	// Scope distinguishes the two halves of a file that is stored twice —
	// FFXIV.cfg, whose shared and profile parts otherwise appear as the same
	// path listed twice with no way to tell them apart.
	Scope string

	Note string
}

func (c Change) String() string {
	name := c.Logical
	if c.Scope != "" {
		name += " [" + c.Scope + "]"
	}

	return fmt.Sprintf("  %-56s %s", name, c.Note)
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
		out = append(out, change.String())
	}
	for _, change := range r.Conflicts {
		out = append(out, Change{Logical: change.Logical, Scope: change.Scope, Note: "CONFLICT: " + change.Note}.String())
	}

	sort.Strings(out)
	return out
}

// Files counts what a run touched as a person would count it: FFXIV.cfg is one
// file however many halves it is stored in.
func (r Report) Files() int {
	seen := map[string]bool{}
	for _, change := range r.Changed {
		seen[change.Logical] = true
	}

	return len(seen)
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
	if scope, ok := machineSpecificPlugins[name]; ok {
		return scope
	}

	return cfg.Shared
}

// machineSpecificPlugins keep their settings at home unless the config says
// otherwise. Penumbra's are a mod root path and collection GUIDs that are
// generated per installation: carrying them sets another machine's Default and
// Interface collections to identifiers it has never seen, which reads as "None"
// and silently switches every UI mod off.
var machineSpecificPlugins = map[string]cfg.Scope{
	"Penumbra": cfg.Local,
}

// Scan reads every file the policy has an opinion about.
func Scan(opts Options) ([]File, []Change, error) {
	var files []File
	var skipped []Change

	collect := func(logical string, data []byte, modTime time.Time, action policy.Action, key, scope string) {
		files = append(files, File{
			Logical: logical,
			Key:     key,
			Scope:   scope,
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
					collect(logical, part, info.ModTime().UTC(), action,
						manifest.Key(logical, where, opts.Profile), scope.String())
				}

			default:
				collect(logical, data, info.ModTime().UTC(), action,
					manifest.Key(logical, action, opts.Profile), "")
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

			logical := policy.Dalamud + "/dalamudConfig.json"
			switch {
			case repos == nil || dalamud.Count(repos) == 0:
				// A Dalamud that has just been installed writes a config with
				// no custom repositories. Pushing that would wipe the real list
				// for every other machine, so an empty one never travels.
				skipped = append(skipped, Change{
					Logical: logical,
					Note:    "no custom repositories here yet; not overwriting the stored list",
				})
			default:
				collect(logical, repos, info.ModTime().UTC(), policy.Repos,
					manifest.Key(logical, policy.Sync, opts.Profile), "")
			}

			// Which plugins are enabled, stored for "ffsync plugins" and never
			// written back to any machine.
			plugins, err := dalamud.Plugins(data)
			if err != nil {
				return nil, skipped, err
			}
			if len(plugins) > 0 {
				listed, err := json.MarshalIndent(plugins, "", "  ")
				if err != nil {
					return nil, skipped, err
				}

				name := policy.Dalamud + "/plugins.json"
				collect(name, listed, info.ModTime().UTC(), policy.List,
					manifest.DeviceKey(name, opts.Device), "")
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
				Scope:   file.Scope,
				Note: fmt.Sprintf("store has a newer copy from %s (%s)",
					existing.Device, existing.ModTime.Local().Format(time.RFC822)),
			})
			continue
		}

		note := "new"
		if known {
			note = "updated"
		}
		report.Changed = append(report.Changed, Change{Logical: file.Logical, Scope: file.Scope, Note: note})

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
			// Per-device records describe a machine; they are read by
			// "ffsync plugins", never copied onto anybody.
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

		// Stored for reporting only: "ffsync plugins" reads it, nothing writes
		// it to disk.
		if policy.For(logical) == policy.List {
			continue
		}

		scope := ""
		if policy.For(logical) == policy.Merge {
			scope = cfg.Shared.String()
			if _, profiled := manifest.CutProfile(key, opts.Profile); profiled {
				scope = cfg.Profiled.String()
			}
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
				Scope:   scope,
				Note:    "changed here after the store's copy; push, or pull --force",
			})
			continue
		}

		note := "written"
		if !exists {
			note = "new"
		}
		report.Changed = append(report.Changed, Change{Logical: logical, Scope: scope, Note: note})
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

		applied, err := write(opts, logical, data, backed)
		if err != nil {
			return report, fmt.Errorf("%s: %w", logical, err)
		}
		backed[logical] = true

		if len(applied) > 0 {
			report.Changed[len(report.Changed)-1].Note = note + ": " + strings.Join(applied, ", ")
		}
	}

	return report, nil
}

// Write puts one file on disk, keeping a copy of what it replaced. FFXIV.cfg
// and dalamudConfig.json are merged rather than replaced, so this machine keeps
// its own resolution and its own plugin bookkeeping.
func write(opts Options, logical string, data []byte, backed map[string]bool) ([]string, error) {
	target, ok := opts.root(logical)
	if !ok {
		return nil, fmt.Errorf("nowhere to put it on this machine")
	}

	current, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// What the write actually changes, so a report can say "the graphics did
	// arrive" rather than leaving it to a diff against the backup.
	var applied []string

	switch policy.For(logical) {
	case policy.Merge:
		local, incoming := cfg.Parse(current), cfg.Parse(data)
		applied = cfg.Changed(local, incoming, opts.Cfg)
		data = cfg.Merge(local, incoming, opts.Cfg).Bytes()

	case policy.Repos:
		// Nothing to merge into means Dalamud has never run here; writing a
		// config that is only a repository list would lose it the rest.
		if len(current) == 0 {
			return nil, nil
		}
		if data, err = dalamud.Merge(current, data); err != nil {
			return nil, err
		}
	}

	if len(current) > 0 && !backed[logical] {
		if err := os.WriteFile(target+".ffsync-bak", current, 0o644); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}

	return applied, os.WriteFile(target, data, 0o644)
}

// StoredPlugins returns every machine's enabled-plugin list, keyed by device.
// Each machine stores its own: the lists differ on purpose, and a single shared
// one would just be whichever machine pushed last.
func StoredPlugins(ctx context.Context, s store.Store, opts Options) (map[string][]dalamud.Plugin, error) {
	remote, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}

	opener := crypt.NewOpener(opts.Passphrase)
	lists := map[string][]dalamud.Plugin{}

	for key, entry := range remote.Entries {
		device, logical, ok := manifest.CutDevice(key)
		if !ok || logical != policy.Dalamud+"/plugins.json" {
			continue
		}

		stored, err := s.Blob(ctx, entry.Stored())
		if err != nil {
			return nil, err
		}

		data, err := opener.Open(stored)
		if err != nil {
			return nil, err
		}

		var plugins []dalamud.Plugin
		if err := json.Unmarshal(data, &plugins); err != nil {
			return nil, err
		}

		lists[device] = plugins
	}

	return lists, nil
}

// Installed lists the plugins Dalamud has on this machine.
func Installed(opts Options) (map[string]bool, error) {
	installed := map[string]bool{}
	if opts.Roots.InstalledPlugins == "" {
		return installed, nil
	}

	entries, err := os.ReadDir(opts.Roots.InstalledPlugins)
	if os.IsNotExist(err) {
		return installed, nil
	}
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			installed[entry.Name()] = true
		}
	}

	return installed, nil
}
