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

	"github.com/linusfr/ffxiv-sync/internal/config"
	"github.com/linusfr/ffxiv-sync/internal/layout"
	"github.com/linusfr/ffxiv-sync/internal/store"
	"github.com/linusfr/ffxiv-sync/internal/sync"
)

const usage = `ffsync — carry FFXIV settings between machines

  ffsync pull      take the store's settings (run before the game starts)
  ffsync push      send this machine's settings (run after the game exits)
  ffsync status    what is where, and what each direction would do
  ffsync plugins   which plugins the store expects that this machine lacks
  ffsync init      write a starter config

Flags:
  --config PATH    settings file (default: the platform's config directory)
  --force          in a conflict, take this side instead of reporting it
  --dry-run        say what would happen, change nothing
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

	if command == "init" {
		return initialise(path)
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
		return report("pulled", result, err)
	case "push":
		result, err := sync.Push(ctx, backing, options)
		return report("pushed", result, err)
	case "status":
		return status(ctx, backing, settings, options)
	case "plugins":
		return plugins(ctx, backing, options)
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

func report(verb string, result sync.Report, err error) error {
	if err != nil {
		return err
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
		fmt.Printf("cfg      %v\n", settings.Cfg)
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
// machine fetches its own builds from the repositories, which do sync.
func plugins(ctx context.Context, backing store.Store, options sync.Options) error {
	stored, err := sync.StoredPlugins(ctx, backing, options)
	if err != nil {
		return err
	}
	if len(stored) == 0 {
		return fmt.Errorf("the store has no plugin list yet; push from a machine that has Dalamud set up")
	}

	installed, err := sync.Installed(options)
	if err != nil {
		return err
	}

	var missing, disabled []string
	for _, plugin := range stored {
		switch {
		case !installed[plugin.InternalName] && plugin.IsEnabled:
			missing = append(missing, plugin.InternalName)
		case !installed[plugin.InternalName]:
			disabled = append(disabled, plugin.InternalName)
		}
	}

	fmt.Printf("stored list: %d plugins, %d installed here\n", len(stored), len(installed))

	if len(missing) > 0 {
		fmt.Printf("\nmissing (enabled elsewhere): %d\n", len(missing))
		for _, name := range missing {
			fmt.Println("  " + name)
		}
		fmt.Println("\nInstall them from Dalamud's plugin installer — the custom repositories")
		fmt.Println("they come from are already synced. Their settings are waiting too.")
	}

	if len(disabled) > 0 {
		fmt.Printf("\nmissing, but switched off where the list came from: %d\n", len(disabled))
		for _, name := range disabled {
			fmt.Println("  " + name)
		}
	}

	if len(missing) == 0 && len(disabled) == 0 {
		fmt.Println("nothing missing.")
	}

	return nil
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
