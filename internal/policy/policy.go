// Package policy decides what happens to each file: travel between machines,
// stay with one kind of machine, or never leave at all.
package policy

import (
	"path"
	"strings"
)

// The three namespaces a logical path starts with. They say which root a file
// came from, so one manifest can carry the game's settings and Dalamud's.
const (
	Game    = "game"
	Plugins = "plugins"
	Dalamud = "dalamud"
)

// Action is what sync does with a file.
type Action int

const (
	// Skip leaves the file alone entirely.
	Skip Action = iota

	// Sync shares one copy between every machine.
	Sync

	// Profile keeps one copy per profile, because the setting only makes sense
	// for machines of the same shape: a handheld's HUD layout is built for its
	// screen and its gamepad, and would be nonsense on a desktop.
	Profile

	// Merge is for FFXIV.cfg, which is one text file holding both settings worth
	// sharing and settings that describe the machine.
	Merge

	// Repos is for dalamudConfig.json, of which only the custom repository list
	// travels; the rest of that file is this machine's own state.
	Repos

	// List is stored and read back for reporting, never written to disk. Which
	// plugins are enabled is worth knowing on a new machine, but installing
	// them is Dalamud's job.
	List
)

func (a Action) String() string {
	switch a {
	case Sync:
		return "sync"
	case Profile:
		return "profile"
	case Merge:
		return "merge"
	case Repos:
		return "repos"
	case List:
		return "list"
	default:
		return "skip"
	}
}

type rule struct {
	pattern string
	action  Action
}

// Rules are matched in order, first hit wins. Patterns use path.Match against
// the logical path, with a trailing "/**" matching a whole subtree.
var rules = []rule{
	// Backups, rotated copies and logs: the game's own, not ours to move.
	{"**.old", Skip},
	{"game/backup/**", Skip},
	{"game/cfgcopy/**", Skip},
	{"game/screenshots/**", Skip},
	{"game/*/log/**", Skip},

	// The machine describes itself in here, so it is merged key by key.
	{"game/FFXIV.cfg", Merge},

	// Worth carrying: everything that is tedious to rebuild by hand.
	{"game/MACROSYS.dat", Sync},
	{"game/*/HOTBAR.DAT", Sync},
	{"game/*/MACRO.DAT", Sync},
	{"game/*/GEARSET.DAT", Sync},
	{"game/*/ITEMODR.DAT", Sync},
	{"game/*/ITEMFDR.DAT", Sync},
	{"game/*/ACQ.DAT", Sync},
	{"game/*/GS.DAT", Sync},
	{"game/*/LOGFLTR.DAT", Sync},

	// Shaped by the screen and the input device.
	{"game/*/ADDON.DAT", Profile},
	{"game/*/UISAVE.DAT", Profile},
	{"game/*/KEYBIND.DAT", Profile},
	{"game/*/CONTROL0.DAT", Profile},
	{"game/*/CONTROL1.DAT", Profile},
	{"game/*/COMMON.DAT", Profile},

	// Only the repository list, merged into whatever this machine has.
	{"dalamud/dalamudConfig.json", Repos},

	// Which plugins are enabled, for "ffsync plugins". Never written back.
	{"dalamud/plugins.json", List},

	// A plugin's settings are JSON, either beside the others or in the
	// plugin's own directory. Everything else it keeps there — caches, replays,
	// databases, logs, lock files — is local, rebuilt on demand, and in one
	// case 500 MB.
	{"plugins/*.json", Sync},
	{"plugins/*/*.json", Sync},
	{"plugins/*/*/**", Skip},
	{"plugins/**", Skip},
}

// For reports what should happen to a logical path. Paths start with one of the
// namespace constants and always use forward slashes.
func For(logical string) Action {
	for _, r := range rules {
		if match(r.pattern, logical) {
			return r.action
		}
	}

	// Anything new stays put until someone decides otherwise.
	return Skip
}

// skipTrees are directories a walk can turn around at. They are listed rather
// than derived from the rules, because the last rule is a catch-all: everything
// under plugins/ is skipped *except* what the rules above it pick out, and a
// walk still has to go in to find those.
var skipTrees = []string{
	"game/backup",
	"game/cfgcopy",
	"game/screenshots",
	"game/*/log",

	// A plugin's own subdirectories: caches, replays, databases, logs. Its
	// settings are JSON one level up.
	"plugins/*/*",
}

// SkipTree reports whether a directory is one of those, so a 500 MB cache is
// never walked into.
func SkipTree(logical string) bool {
	for _, subtree := range skipTrees {
		if matchSegments(subtree, logical) {
			return true
		}
	}

	return false
}

func match(pattern, logical string) bool {
	if strings.HasPrefix(pattern, "**") {
		return strings.HasSuffix(logical, strings.TrimPrefix(pattern, "**"))
	}

	if subtree, ok := strings.CutSuffix(pattern, "/**"); ok {
		if before, found := strings.CutSuffix(subtree, "**"); found {
			return matchSegments(strings.TrimSuffix(before, "/"), logical)
		}
		if ok, _ := path.Match(subtree, logical); ok {
			return true
		}

		return matchSegments(subtree, logical) && len(strings.Split(logical, "/")) > len(strings.Split(subtree, "/"))
	}

	if before, found := strings.CutSuffix(pattern, "**"); found {
		return matchSegments(strings.TrimSuffix(before, "/"), logical)
	}

	ok, _ := path.Match(pattern, logical)
	return ok
}

// MatchSegments matches a prefix of the path segment by segment, which is what
// a subtree pattern with a wildcard in it needs and path.Match cannot express.
func matchSegments(subtree, logical string) bool {
	want := strings.Split(subtree, "/")
	have := strings.Split(logical, "/")
	if len(have) < len(want) {
		return false
	}

	for i, segment := range want {
		if ok, _ := path.Match(segment, have[i]); !ok {
			return false
		}
	}

	return true
}
