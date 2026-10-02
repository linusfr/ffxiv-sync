package policy

import "testing"

func TestFor(t *testing.T) {
	cases := map[string]Action{
		"game/FFXIV.cfg":                                  Merge,
		"game/MACROSYS.dat":                               Sync,
		"game/FFXIV_CHR0040002E/HOTBAR.DAT":               Sync,
		"game/FFXIV_CHR0040002E/GEARSET.DAT":              Sync,
		"game/FFXIV_CHR0040002E/ADDON.DAT":                Profile,
		"game/FFXIV_CHR0040002E/KEYBIND.DAT":              Profile,
		"game/FFXIV_CHR0040002E/CONTROL1.DAT":             Profile,
		"game/FFXIV_CHR0040002E/HOTBAR.DAT.old":           Skip,
		"FFXIV.cfg.old":                                   Skip,
		"game/backup/FFXIV.cfg":                           Skip,
		"game/cfgcopy/whatever":                           Skip,
		"game/screenshots/shot.png":                       Skip,
		"game/FFXIV_CHR0040002E/log/chat.log":             Skip,
		"game/something.new":                              Skip,
		"dalamud/dalamudConfig.json":                      Repos,
		"plugins/BossMod.json":                            Sync,
		"plugins/AutoRetainer/DefaultConfig.json":         Sync,
		"plugins/vnavmesh/meshcache/x.bin":                Skip,
		"plugins/Browsingway/dependencies/cef/libcef.dll": Skip,
		"plugins/MinimalMeter/combat_logs/2026.log":       Skip,
		"plugins/BossMod.json.bak":                        Skip,
	}

	for path, want := range cases {
		if got := For(path); got != want {
			t.Errorf("For(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestSkipTree(t *testing.T) {
	skipped := []string{
		"game/backup", "game/screenshots", "game/cfgcopy",
		"game/FFXIV_CHR0040002E/log",
		"plugins/vnavmesh/meshcache", "plugins/Browsingway/cef-cache",
		"plugins/MinimalMeter/combat_logs", "plugins/BossMod/replays",
	}
	for _, dir := range skipped {
		if !SkipTree(dir) {
			t.Errorf("SkipTree(%q) = false, want true", dir)
		}
	}

	// A directory holding real settings has to be walked into, which is what
	// inferring the answer from a made-up child name got wrong.
	for _, dir := range []string{"game/FFXIV_CHR0040002E", "plugins/AutoRetainer", "plugins"} {
		if SkipTree(dir) {
			t.Errorf("SkipTree(%q) = true, want false", dir)
		}
	}
}
