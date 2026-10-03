package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/cfg"
	"github.com/linusfr/ffxiv-sync/internal/layout"
	"github.com/linusfr/ffxiv-sync/internal/store"
)

const character = "FFXIV_CHR0040002E93BD27D3"

func machine(t *testing.T, screenWidth, hotbar, addon string) layout.Roots {
	t.Helper()

	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "FFXIV.cfg"), "<FINAL FANTASY XIV Config File>\n\n"+
		"<Display Settings>\nScreenWidth\t"+screenWidth+"\n\n"+
		"<Graphics Settings>\nSSAO\t"+screenWidth+"\n\n"+
		"<Cutscene Settings>\nCutsceneMovieVoice\t90\n")
	writeFixture(t, filepath.Join(root, character, "HOTBAR.DAT"), hotbar)
	writeFixture(t, filepath.Join(root, character, "ADDON.DAT"), addon)
	writeFixture(t, filepath.Join(root, character, "HOTBAR.DAT.old"), "stale")
	writeFixture(t, filepath.Join(root, "backup", "FFXIV.cfg"), "old copy")

	return layout.Roots{GameConfig: root}
}

func writeFixture(t *testing.T, path, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// A desktop pushes, a handheld pulls: shared files travel, profile files do
// not, and the handheld keeps its own resolution.
func TestPushThenPullAcrossProfiles(t *testing.T) {
	ctx := context.Background()
	backing := &store.Dir{Root: t.TempDir()}

	desktop := Options{Roots: machine(t, "3840", "desktop hotbars", "desktop hud"), Profile: "desktop", Device: "tower"}
	handheld := Options{Roots: machine(t, "1280", "handheld hotbars", "handheld hud"), Profile: "handheld", Device: "deck"}

	pushed, err := Push(ctx, backing, desktop)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed.Changed) == 0 {
		t.Fatal("first push sent nothing")
	}

	// The handheld's files were written after the desktop's, so without this
	// the conflict rule would hold the pull back; that rule has its own test.
	future := time.Now().Add(-time.Hour)
	for _, name := range []string{"FFXIV.cfg", filepath.Join(character, "HOTBAR.DAT"), filepath.Join(character, "ADDON.DAT")} {
		path := filepath.Join(handheld.Roots.GameConfig, name)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Pull(ctx, backing, handheld); err != nil {
		t.Fatal(err)
	}

	if got := read(t, filepath.Join(handheld.Roots.GameConfig, character, "HOTBAR.DAT")); got != "desktop hotbars" {
		t.Errorf("hotbars did not travel: %q", got)
	}
	if got := read(t, filepath.Join(handheld.Roots.GameConfig, character, "ADDON.DAT")); got != "handheld hud" {
		t.Errorf("the desktop's HUD reached the handheld: %q", got)
	}

	merged := read(t, filepath.Join(handheld.Roots.GameConfig, "FFXIV.cfg"))
	if !strings.Contains(merged, "ScreenWidth\t1280") {
		t.Errorf("the handheld lost its resolution:\n%s", merged)
	}
}

// Pulling over a file this machine changed more recently would throw that
// change away, so it is reported instead.
func TestPullReportsConflict(t *testing.T) {
	ctx := context.Background()
	backing := &store.Dir{Root: t.TempDir()}

	desktop := Options{Roots: machine(t, "3840", "desktop hotbars", "hud"), Profile: "desktop", Device: "tower"}
	if _, err := Push(ctx, backing, desktop); err != nil {
		t.Fatal(err)
	}

	laptop := Options{Roots: machine(t, "1920", "laptop hotbars", "hud"), Profile: "desktop", Device: "laptop"}
	result, err := Pull(ctx, backing, laptop)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected a conflict")
	}
	if got := read(t, filepath.Join(laptop.Roots.GameConfig, character, "HOTBAR.DAT")); got != "laptop hotbars" {
		t.Errorf("conflicting file was overwritten anyway: %q", got)
	}

	laptop.Force = true
	if _, err := Pull(ctx, backing, laptop); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(laptop.Roots.GameConfig, character, "HOTBAR.DAT")); got != "desktop hotbars" {
		t.Errorf("--force did not take the store's copy: %q", got)
	}
	if got := read(t, filepath.Join(laptop.Roots.GameConfig, character, "HOTBAR.DAT.ffsync-bak")); got != "laptop hotbars" {
		t.Errorf("no backup of what was replaced: %q", got)
	}
}

// Two machines pushing from the same generation: the second has to pull first.
func TestCommitRefusesStalePush(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	first := &store.Dir{Root: root}
	if _, err := Push(ctx, first, Options{
		Roots: machine(t, "3840", "a", "a"), Profile: "desktop", Device: "tower",
	}); err != nil {
		t.Fatal(err)
	}

	current, err := first.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current.Generation = 1 // what a machine that never saw the first push would send

	if err := first.Commit(ctx, current, nil); err != store.ErrStale {
		t.Fatalf("stale commit returned %v, want ErrStale", err)
	}
}

// A folder a sync tool is still arguing over is not safe to read.
func TestDirRefusesSyncConflicts(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "current.sync-conflict-20260101-120000-ABCDEFG.json"), "{}")

	if _, err := (&store.Dir{Root: root}).Current(context.Background()); err == nil {
		t.Fatal("expected a refusal")
	}
}

// Graphics stay put by default, and travel between machines of the same shape
// once asked to. A handheld never sees them either way.
func TestGraphicsTravelOnlyWhenAskedTo(t *testing.T) {
	ctx := context.Background()

	for _, c := range []struct {
		name     string
		policy   cfg.Policy
		want     string
		handheld string
	}{
		{"default", cfg.Policy{}, "1920", "1280"},
		{"opted in", cfg.Policy{Overrides: map[string]cfg.Scope{"Graphics Settings": cfg.Profiled}}, "3840", "1280"},
	} {
		t.Run(c.name, func(t *testing.T) {
			backing := &store.Dir{Root: t.TempDir()}

			tower := Options{Roots: machine(t, "3840", "hotbars", "hud"), Profile: "desktop", Device: "tower", Cfg: c.policy}
			if _, err := Push(ctx, backing, tower); err != nil {
				t.Fatal(err)
			}

			laptop := Options{Roots: machine(t, "1920", "hotbars", "hud"), Profile: "desktop", Device: "laptop", Cfg: c.policy, Force: true}
			if _, err := Pull(ctx, backing, laptop); err != nil {
				t.Fatal(err)
			}

			deck := Options{Roots: machine(t, "1280", "hotbars", "hud"), Profile: "handheld", Device: "deck", Cfg: c.policy, Force: true}
			if _, err := Pull(ctx, backing, deck); err != nil {
				t.Fatal(err)
			}

			if got := read(t, filepath.Join(laptop.Roots.GameConfig, "FFXIV.cfg")); !strings.Contains(got, "SSAO\t"+c.want) {
				t.Errorf("laptop graphics: want SSAO %s, got:\n%s", c.want, got)
			}
			if got := read(t, filepath.Join(deck.Roots.GameConfig, "FFXIV.cfg")); !strings.Contains(got, "SSAO\t"+c.handheld) {
				t.Errorf("handheld graphics: want SSAO %s, got:\n%s", c.handheld, got)
			}
			if got := read(t, filepath.Join(laptop.Roots.GameConfig, "FFXIV.cfg")); !strings.Contains(got, "ScreenWidth\t1920") {
				t.Error("resolution travelled, which it never should")
			}
		})
	}
}

// Plugin settings travel, their caches do not, and a plugin can be pinned to a
// profile or kept at home.
func TestPluginSettings(t *testing.T) {
	ctx := context.Background()
	backing := &store.Dir{Root: t.TempDir()}

	build := func(t *testing.T, mark string) layout.Roots {
		roots := machine(t, "1920", "hotbars", "hud")
		dir := t.TempDir()

		writeFixture(t, filepath.Join(dir, "pluginConfigs", "BossMod.json"), `{"mark":"`+mark+`"}`)
		writeFixture(t, filepath.Join(dir, "pluginConfigs", "MinimalMeter.json"), `{"window":"`+mark+`"}`)
		writeFixture(t, filepath.Join(dir, "pluginConfigs", "AutoRetainer", "DefaultConfig.json"), `{"mark":"`+mark+`"}`)
		writeFixture(t, filepath.Join(dir, "pluginConfigs", "vnavmesh", "meshcache", "big.bin"), strings.Repeat("x", 4096))
		writeFixture(t, filepath.Join(dir, "pluginConfigs", "Huge.json"), strings.Repeat("x", 3<<20))
		writeFixture(t, filepath.Join(dir, "dalamudConfig.json"),
			`{"DoPluginTest":false,"ThirdRepoList":{"$values":[{"Url":"https://`+mark+`.example/repo.json","IsEnabled":true}]}}`)

		roots.PluginConfigs = filepath.Join(dir, "pluginConfigs")
		roots.DalamudConfig = filepath.Join(dir, "dalamudConfig.json")

		return roots
	}

	scopes := map[string]cfg.Scope{"MinimalMeter": cfg.Profiled}

	tower := Options{Roots: build(t, "tower"), Profile: "desktop", Device: "tower", Plugins: scopes}
	pushed, err := Push(ctx, backing, tower)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed.Skipped) != 1 || !strings.Contains(pushed.Skipped[0].Logical, "Huge.json") {
		t.Errorf("size cap skipped %v, want just Huge.json", pushed.Skipped)
	}

	deck := Options{Roots: build(t, "deck"), Profile: "handheld", Device: "deck", Plugins: scopes, Force: true}
	if _, err := Pull(ctx, backing, deck); err != nil {
		t.Fatal(err)
	}

	if got := read(t, filepath.Join(deck.Roots.PluginConfigs, "BossMod.json")); !strings.Contains(got, "tower") {
		t.Errorf("plugin settings did not travel: %s", got)
	}
	if got := read(t, filepath.Join(deck.Roots.PluginConfigs, "AutoRetainer", "DefaultConfig.json")); !strings.Contains(got, "tower") {
		t.Errorf("a plugin's config directory did not travel: %s", got)
	}
	if got := read(t, filepath.Join(deck.Roots.PluginConfigs, "MinimalMeter.json")); !strings.Contains(got, "deck") {
		t.Errorf("a profile-scoped plugin took the desktop's settings: %s", got)
	}
	if _, err := os.Stat(filepath.Join(deck.Roots.PluginConfigs, "vnavmesh", "meshcache", "big.bin")); err != nil {
		t.Error("the cache file went missing, so something is writing where it should not")
	}

	// The repository list is merged in; the rest of Dalamud's config is not.
	merged := read(t, deck.Roots.DalamudConfig)
	if !strings.Contains(merged, "tower.example") || !strings.Contains(merged, "deck.example") {
		t.Errorf("repositories were not merged: %s", merged)
	}
	if !strings.Contains(merged, "DoPluginTest") {
		t.Error("merging the repository list rewrote the rest of the file")
	}
}

// With a passphrase set, the store holds nothing readable.
func TestEncryptedStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	backing := &store.Dir{Root: root}

	tower := Options{
		Roots:   machine(t, "1920", "secret hotbars", "hud"),
		Profile: "desktop", Device: "tower", Passphrase: "hunter2",
	}
	if _, err := Push(ctx, backing, tower); err != nil {
		t.Fatal(err)
	}

	found := false
	filepath.WalkDir(filepath.Join(root, "blobs"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "secret hotbars") {
			found = true
		}
		return nil
	})
	if found {
		t.Error("the store holds readable settings")
	}

	deck := Options{
		Roots:   machine(t, "1280", "other", "hud"),
		Profile: "desktop", Device: "deck", Passphrase: "hunter2", Force: true,
	}
	if _, err := Pull(ctx, backing, deck); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(deck.Roots.GameConfig, character, "HOTBAR.DAT")); got != "secret hotbars" {
		t.Errorf("decrypted to %q", got)
	}

	wrong := deck
	wrong.Roots = machine(t, "1280", "other", "hud")
	wrong.Passphrase = "nope"
	if _, err := Pull(ctx, backing, wrong); err == nil {
		t.Error("the wrong passphrase was accepted")
	}
}

// A freshly installed Dalamud has no custom repositories. Pushing that would
// wipe the list for every other machine.
func TestEmptyRepoListIsNotPushed(t *testing.T) {
	ctx := context.Background()
	backing := &store.Dir{Root: t.TempDir()}

	withConfig := func(t *testing.T, repos string) Options {
		roots := machine(t, "1920", "hotbars", "hud")
		dir := t.TempDir()
		writeFixture(t, filepath.Join(dir, "dalamudConfig.json"),
			`{"DoPluginTest":false,"ThirdRepoList":{"$values":[`+repos+`]},`+
				`"DefaultProfile":{"Plugins":{"$values":[{"InternalName":"BossMod","IsEnabled":true}]}}}`)
		writeFixture(t, filepath.Join(dir, "pluginConfigs", "BossMod.json"), `{"mark":"x"}`)

		roots.PluginConfigs = filepath.Join(dir, "pluginConfigs")
		roots.DalamudConfig = filepath.Join(dir, "dalamudConfig.json")

		return Options{Roots: roots, Profile: "desktop", Device: "tower", Force: true}
	}

	full := withConfig(t, `{"Url":"https://example.com/one.json","IsEnabled":true}`)
	if _, err := Push(ctx, backing, full); err != nil {
		t.Fatal(err)
	}

	fresh := withConfig(t, "")
	fresh.Device = "newmachine"
	report, err := Push(ctx, backing, fresh)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, change := range report.Skipped {
		if strings.Contains(change.Logical, "dalamudConfig.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("an empty repository list was pushed; skipped: %v", report.Skipped)
	}

	// And the stored list still has the repository in it.
	current, err := backing.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := current.Entries["shared/dalamud/dalamudConfig.json"]; !ok {
		t.Error("the stored repository list went missing")
	}

	// The plugin list is stored but never written back to a machine.
	if _, ok := current.Entries["shared/dalamud/plugins.json"]; !ok {
		t.Fatal("the plugin list was not stored")
	}
	plugins, err := StoredPlugins(ctx, backing, fresh)
	if err != nil || len(plugins) != 1 || plugins[0].InternalName != "BossMod" {
		t.Fatalf("stored plugins = %+v, err %v", plugins, err)
	}

	target := Options{Roots: machine(t, "1280", "other", "hud"), Profile: "handheld", Device: "deck", Force: true}
	if _, err := Pull(ctx, backing, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target.Roots.GameConfig, "plugins.json")); err == nil {
		t.Error("the plugin list was written to disk")
	}
}

// FFXIV.cfg is stored in two halves but is one file to a person.
func TestReportCountsFilesNotBlobs(t *testing.T) {
	report := Report{Changed: []Change{
		{Logical: "game/FFXIV.cfg", Scope: "shared", Note: "new"},
		{Logical: "game/FFXIV.cfg", Scope: "profile", Note: "new"},
		{Logical: "game/MACROSYS.dat", Note: "new"},
	}}

	if report.Files() != 2 {
		t.Errorf("Files() = %d, want 2", report.Files())
	}
	for _, line := range report.Lines() {
		if strings.Contains(line, "FFXIV.cfg") && !strings.Contains(line, "[") {
			t.Errorf("the two halves are indistinguishable: %q", line)
		}
	}
}
