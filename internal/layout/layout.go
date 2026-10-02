// Package layout finds the directories the game and Dalamud keep settings in,
// which sit in a different place on every launcher and platform.
package layout

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Roots are the directories ffsync reads and writes. Dalamud's are optional:
// the game's settings sync on their own if it is not installed.
type Roots struct {
	// GameConfig holds FFXIV.cfg, MACROSYS.dat and one FFXIV_CHR* directory
	// per character.
	GameConfig string

	// PluginConfigs holds one JSON file, and sometimes one directory, per
	// installed plugin.
	PluginConfigs string

	// DalamudConfig is the file holding Dalamud's own settings, of which only
	// the custom repository list travels.
	DalamudConfig string
}

// ErrNotFound means no known layout matched; the user has to say where it is.
var ErrNotFound = errors.New("no FFXIV config directory found")

// Detect returns the first layout that exists on this machine. The game's
// directory and Dalamud's are found separately, because on Windows they are
// nowhere near each other.
func Detect() (Roots, error) {
	roots := Roots{}

	for _, candidate := range gameCandidates() {
		if exists(filepath.Join(candidate, "FFXIV.cfg")) {
			roots.GameConfig = candidate
			break
		}
	}
	if roots.GameConfig == "" {
		return roots, ErrNotFound
	}

	for _, candidate := range dalamudCandidates() {
		if exists(filepath.Join(candidate, "dalamudConfig.json")) {
			roots.PluginConfigs = filepath.Join(candidate, "pluginConfigs")
			roots.DalamudConfig = filepath.Join(candidate, "dalamudConfig.json")
			break
		}
	}

	return roots, nil
}

// GameCandidates lists where the game's own config lives, most likely first. It
// is exported so "ffsync status" can show what was searched.
func GameCandidates() []string { return gameCandidates() }

// DalamudCandidates lists where XIVLauncher keeps Dalamud's files.
func DalamudCandidates() []string { return dalamudCandidates() }

func gameCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		// The launcher keeps its own files in AppData, but the game writes its
		// config under Documents, where OneDrive may have moved it.
		const game = "FINAL FANTASY XIV - A Realm Reborn"
		return []string{
			filepath.Join(home, "Documents", "My Games", game),
			filepath.Join(home, "OneDrive", "Documents", "My Games", game),
		}

	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "XIV on Mac", "game",
				"drive_c", "users", "crossover", "Documents", "My Games", "FINAL FANTASY XIV - A Realm Reborn"),
			filepath.Join(home, ".xlcore", "ffxivConfig"),
		}

	default:
		return []string{
			filepath.Join(home, ".xlcore", "ffxivConfig"),
			// Flatpak XIVLauncher.Core, which the Steam Deck installs by default.
			filepath.Join(home, ".var", "app", "dev.goats.xivlauncher", "data", "xlcore", "ffxivConfig"),
			filepath.Join(home, ".var", "app", "dev.goats.xivlauncher", "config", "xlcore", "ffxivConfig"),
		}
	}
}

func dalamudCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		return []string{filepath.Join(os.Getenv("APPDATA"), "XIVLauncher")}

	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "XIV on Mac", "Dalamud"),
			filepath.Join(home, ".xlcore"),
		}

	default:
		return []string{
			filepath.Join(home, ".xlcore"),
			filepath.Join(home, ".var", "app", "dev.goats.xivlauncher", "data", "xlcore"),
			filepath.Join(home, ".var", "app", "dev.goats.xivlauncher", "config", "xlcore"),
		}
	}
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
