// Command ffsync carries FFXIV's settings between machines. It runs around the
// game rather than inside it: the game loads its config at launch and writes it
// back at exit, so anything written while it runs is thrown away.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/linusfr/ffxiv-sync/internal/config"
	"github.com/linusfr/ffxiv-sync/internal/layout"
	"github.com/linusfr/ffxiv-sync/internal/selfupdate"
	"github.com/linusfr/ffxiv-sync/internal/store"
	"github.com/linusfr/ffxiv-sync/internal/sync"
)

// Version is set at build time: go build -ldflags "-X main.version=1.2.0".
// "dev" means a local build, which "update" refuses to replace.
var version = "dev"

const usage = `ffsync — carry FFXIV settings between machines

  ffsync pull      take the store's settings (run before the game starts)
  ffsync push      send this machine's settings (run after the game exits)
  ffsync status    what is where, and what each direction would do
  ffsync plugins   which plugins the store expects that this machine lacks
                   (--all for every plugin and which machine lists it)
  ffsync version   what this is, and whether a newer release exists
  ffsync update    replace this binary with the newest release
  ffsync init      write a starter config

Flags:
  --config PATH    settings file (default: the platform's config directory)
  --force          in a conflict, take this side instead of reporting it
  --dry-run        say what would happen, change nothing
  --check          with version or update: report, download nothing
  --all            with plugins: every plugin and which machine lists it

What travels is set in the config file, not here: "cfg" is what this machine
shares, "cfg_apply" what it takes. "ffsync status" prints both. Graphics,
resolution and input hardware stay local until named on both ends.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ffsync:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("ffsync", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	configPath := flags.String("config", "", "settings file")
	force := flags.Bool("force", false, "take this side in a conflict")
	dryRun := flags.Bool("dry-run", false, "change nothing")
	all := flags.Bool("all", false, "with plugins: every plugin and where it is")
	check := flags.Bool("check", false, "with update: report what would be installed")

	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	command := flags.Arg(0)
	if command == "" {
		flags.Usage()
		return fmt.Errorf("no command given")
	}

	// Flags after the command too: "ffsync pull --force" is what the conflict
	// message tells people to run, and Go's flag package stops at the first
	// argument that is not one.
	if err := flags.Parse(flags.Args()[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	path := *configPath
	if path == "" {
		var err error
		if path, err = config.Path(); err != nil {
			return err
		}
	}

	switch command {
	// Typed often enough to be worth answering rather than rejecting.
	case "help":
		flags.Usage()
		return nil
	case "init":
		return initialise(path)
	case "version":
		return printVersion(context.Background(), *check)
	case "update":
		return update(context.Background(), *check)
	}

	settings, err := config.Load(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("no settings at %s — run \"ffsync init\"", path)
	}
	if err != nil {
		return err
	}

	roots, err := resolve(settings)
	if err != nil {
		return err
	}

	cfgPolicy, err := settings.Policy()
	if err != nil {
		return err
	}
	pluginScopes, err := settings.PluginScopes()
	if err != nil {
		return err
	}
	passphrase, err := settings.Secret()
	if err != nil {
		return err
	}

	options := sync.Options{
		Roots:        roots,
		Cfg:          cfgPolicy,
		Plugins:      pluginScopes,
		Passphrase:   passphrase,
		MaxFileBytes: settings.MaxFileKB * 1024,
		Profile:      settings.Profile,
		Device:       settings.Device,
		Force:        *force,
		DryRun:       *dryRun,
	}

	backing, err := open(settings)
	if err != nil {
		return err
	}

	ctx := context.Background()
	switch command {
	case "pull":
		result, err := sync.Pull(ctx, backing, options)
		return report("pulled", *dryRun, result, err)
	case "push":
		result, err := sync.Push(ctx, backing, options)
		return report("pushed", *dryRun, result, err)
	case "status":
		return status(ctx, backing, settings, options)
	case "plugins":
		return plugins(ctx, backing, options, *all)
	default:
		flags.Usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func resolve(settings *config.Config) (layout.Roots, error) {
	roots, err := layout.Detect()
	if err != nil && settings.GameConfig == "" {
		fmt.Fprintln(os.Stderr, "looked in:")
		for _, candidate := range layout.GameCandidates() {
			fmt.Fprintln(os.Stderr, "  "+candidate)
		}
		return roots, fmt.Errorf("%w — set game_config in the settings file", err)
	}
	if settings.GameConfig != "" {
		roots.GameConfig = settings.GameConfig
	}

	return roots, nil
}

func open(settings *config.Config) (store.Store, error) {
	switch settings.Store.Kind {
	case "dir":
		if settings.Store.Path == "" {
			return nil, fmt.Errorf("store.path is empty")
		}
		return &store.Dir{Root: settings.Store.Path}, nil

	case "http":
		if settings.Store.URL == "" {
			return nil, fmt.Errorf("store.url is empty")
		}
		return &store.HTTP{Base: settings.Store.URL, Token: settings.Store.Token}, nil

	default:
		return nil, fmt.Errorf("store.kind must be \"dir\" or \"http\", not %q", settings.Store.Kind)
	}
}

func report(verb string, dryRun bool, result sync.Report, err error) error {
	if err != nil {
		return err
	}
	if dryRun {
		verb = "would " + strings.TrimSuffix(verb, "ed")
	}

	for _, line := range result.Lines() {
		fmt.Println(line)
	}

	fmt.Printf("%s: %d file(s) changed, %d unchanged, %d conflicts, %d skipped, generation %d\n",
		verb, result.Files(), result.Unchanged, len(result.Conflicts), len(result.Skipped), result.Generation)

	if len(result.Conflicts) > 0 {
		return fmt.Errorf("%d conflicts left alone; resolve them or use --force", len(result.Conflicts))
	}

	return nil
}

func status(ctx context.Context, backing store.Store, settings *config.Config, options sync.Options) error {
	fmt.Printf("device   %s (profile %s)\n", settings.Device, settings.Profile)
	fmt.Printf("game     %s\n", options.Roots.GameConfig)

	if options.Roots.DalamudConfig == "" {
		fmt.Printf("dalamud  %s\n", options.Roots.DalamudHint)
	} else {
		fmt.Printf("dalamud  %s\n", options.Roots.DalamudConfig)
	}
	if options.Passphrase != "" {
		fmt.Println("blobs    encrypted")
	}

	where := settings.Store.Path
	if settings.Store.Kind == "http" {
		where = settings.Store.URL
	}
	fmt.Printf("store    %s %s\n", settings.Store.Kind, where)

	if len(settings.Cfg) > 0 {
		fmt.Printf("cfg      shares %v\n", settings.Cfg)
	}
	if len(settings.CfgApply) > 0 {
		fmt.Printf("cfg      applies %v\n", applied(settings.CfgApply))
	} else {
		fmt.Println("cfg      applies nothing machine-specific; set cfg_apply to take graphics or input settings")
	}

	current, err := backing.Current(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("stored   generation %d, %d files, last written by %s\n",
		current.Generation, len(current.Entries), current.Device)

	options.DryRun = true

	pull, err := sync.Pull(ctx, backing, options)
	if err != nil {
		return err
	}
	push, err := sync.Push(ctx, backing, options)
	if err != nil {
		return err
	}

	// Both directions scan, so each would report the same skipped file.
	for _, change := range pull.Skipped {
		fmt.Printf("skipped  %s: %s\n", change.Logical, change.Note)
	}

	fmt.Printf("\npull would change %d file(s):\n", pull.Files())
	for _, line := range pull.Lines() {
		fmt.Println(line)
	}
	fmt.Printf("push would change %d file(s):\n", push.Files())
	for _, line := range push.Lines() {
		fmt.Println(line)
	}

	return nil
}

// Plugins answers the question a machine joining an existing store has: what is
// supposed to be installed here. The plugins themselves are never synced — each
// machine fetches its own builds from the repositories, which do sync — so every
// machine stores its own list and this compares them.
func plugins(ctx context.Context, backing store.Store, options sync.Options, all bool) error {
	lists, err := sync.StoredPlugins(ctx, backing, options)
	if err != nil {
		return err
	}
	if len(lists) == 0 {
		return fmt.Errorf("the store has no plugin list yet; push from a machine that has Dalamud set up")
	}

	installed, err := sync.Installed(options)
	if err != nil {
		return err
	}

	devices := make([]string, 0, len(lists))
	for device := range lists {
		devices = append(devices, device)
	}
	sort.Strings(devices)

	// What every other machine runs, so "missing" means missing from here and
	// not merely absent from one other machine's list.
	elsewhere := map[string]bool{}
	for device, list := range lists {
		if device == options.Device {
			continue
		}
		for _, plugin := range list {
			if plugin.IsEnabled {
				elsewhere[plugin.InternalName] = true
			}
		}
	}

	fmt.Printf("installed here: %d\n", len(installed))
	for _, device := range devices {
		enabled := 0
		for _, plugin := range lists[device] {
			if plugin.IsEnabled {
				enabled++
			}
		}

		here := ""
		if device == options.Device {
			here = " (this machine, as of its last push)"
		}
		fmt.Printf("  %-16s %d listed, %d enabled%s\n", device, len(lists[device]), enabled, here)
	}

	var missing []string
	for name := range elsewhere {
		if !installed[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		fmt.Printf("\nenabled elsewhere, not installed here: %d\n", len(missing))
		for _, name := range missing {
			fmt.Println("  " + name)
		}
		fmt.Println("\nInstall them from Dalamud's plugin installer — the repositories they")
		fmt.Println("come from are already synced, and their settings are waiting too.")
	} else {
		fmt.Println("\nnothing enabled elsewhere is missing here.")
	}

	if !all {
		fmt.Println("\n--all lists every plugin and where it is.")
		return nil
	}

	// The whole picture: one row per plugin, so "what does the other machine
	// have that I do not" and the reverse are both answerable without adding up
	// two lists by hand.
	names := map[string]bool{}
	for name := range installed {
		names[name] = true
	}
	for _, list := range lists {
		for _, plugin := range list {
			names[plugin.InternalName] = true
		}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	fmt.Printf("\n%-28s %-10s %s\n", "plugin", "here", "listed by")
	for _, name := range ordered {
		var listed []string
		for _, device := range devices {
			for _, plugin := range lists[device] {
				if plugin.InternalName != name {
					continue
				}

				mark := device
				if !plugin.IsEnabled {
					mark += " (off)"
				}
				listed = append(listed, mark)
			}
		}

		where := "nowhere"
		if len(listed) > 0 {
			where = strings.Join(listed, ", ")
		}

		state := "missing"
		if installed[name] {
			state = "installed"
		}

		fmt.Printf("%-28s %-10s %s\n", name, state, where)
	}

	return nil
}

// PrintVersion says what is running, and what is available when asked.
func printVersion(ctx context.Context, check bool) error {
	fmt.Printf("ffsync %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)

	if path, err := selfupdate.Target(); err != nil {
		fmt.Printf("binary   %s\n", path)
		fmt.Printf("updates  not from here — %v\n", err)
	} else {
		fmt.Printf("binary   %s\n", path)
	}

	if !check {
		fmt.Println("\n--check asks GitHub what the newest release is.")
		return nil
	}

	release, err := latest(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("latest   %s\n", release.Tag)

	return nil
}

// Update replaces the running binary, or explains why it will not.
func update(ctx context.Context, check bool) error {
	release, err := latest(ctx)
	if err != nil {
		return err
	}

	if release.Tag == version {
		fmt.Printf("ffsync %s is the newest release.\n", version)
		return nil
	}

	name, _, size, ok := release.Asset()
	if !ok {
		return fmt.Errorf("release %s has no build named %s", release.Tag, name)
	}

	fmt.Printf("%s → %s (%s, %.1f MiB)\n", version, release.Tag, name, float64(size)/(1<<20))
	if check {
		fmt.Println("--check only; nothing was downloaded.")
		return nil
	}

	target, err := selfupdate.Target()
	if err != nil {
		return err
	}
	if err := selfupdate.Apply(ctx, release, target); err != nil {
		return err
	}

	fmt.Printf("installed %s at %s\n", release.Tag, target)

	return nil
}

func latest(ctx context.Context) (selfupdate.Release, error) {
	release, err := selfupdate.Latest(ctx)
	if err != nil {
		return release, fmt.Errorf("asking GitHub for the newest release: %w", err)
	}

	return release, nil
}

// Applied is the accepted sections in a stable order, since a map prints in
// whatever order Go feels like and this is meant to be compared between machines.
func applied(accept map[string]bool) []string {
	var sections []string
	for section, yes := range accept {
		if yes {
			sections = append(sections, section)
		}
	}
	sort.Strings(sections)

	return sections
}

func initialise(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}

	host, _ := os.Hostname()
	data, err := json.MarshalIndent(config.Example(host), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}

	fmt.Printf("wrote %s — set store.path (a folder Syncthing replicates) or store.kind \"http\"\n", path)
	return nil
}
