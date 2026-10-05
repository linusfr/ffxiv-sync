# ffsync

[![ci](https://img.shields.io/github/actions/workflow/status/linusfr/ffxiv-sync/ci.yml?branch=main&label=ci&cacheSeconds=300)](https://github.com/linusfr/ffxiv-sync/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/linusfr/ffxiv-sync?label=release&cacheSeconds=300)](https://github.com/linusfr/ffxiv-sync/releases/latest)
[![licence](https://img.shields.io/github/license/linusfr/ffxiv-sync?color=blue)](LICENSE)

> Your hotbars follow you between machines. Your Deck keeps its own HUD.

FFXIV stores settings per installation, so every machine starts from nothing.
ffsync carries the parts worth sharing — hotbars, macros, gearsets, plugin
settings, custom plugin repositories — and leaves the parts that describe the
machine where they are.

It runs around the game, not inside it. The game loads its config at launch and
writes it back at exit, so anything applied mid-session is discarded: `pull`
before the game starts, `push` after it closes.

## Install

Grab the binary for the machine from the
[latest release](https://github.com/linusfr/ffxiv-sync/releases/latest) and put
it on `PATH`. Then:

```sh
ffsync init      # writes a starter config, prints where
ffsync status    # shows what was detected and what each direction would do
ffsync plugins   # which plugins the store expects that this machine lacks
ffsync version   # what is running; --check asks what the newest release is
ffsync update    # replaces this binary with the newest release
```

Updating later is `ffsync update`; see **Keeping it current** below.

The config directory is found automatically for XIVLauncher, XIVLauncher.Core,
its flatpak, and XIV on Mac; `game_config` in the settings file overrides it.

## Settings

`config.json`, in the platform's config directory:

```json
{
  "device": "tower",
  "profile": "desktop",
  "store": { "kind": "dir", "path": "/home/you/Sync/ffxiv" }
}
```

| Field | |
| --- | --- |
| `device` | name in the store's history, defaults to the hostname |
| `profile` | machines with the same screen and controls share a profile |
| `store.kind` | `dir` for a folder, `http` for the server |
| `store.path` | the folder, for `dir` — point it at something Syncthing replicates and no server is needed |
| `store.url` / `store.token` | the server, for `http`; the token can come from `FFSYNC_TOKEN` instead |
| `cfg` | moves `FFXIV.cfg` sections between scopes — what this machine publishes |
| `cfg_apply` | which machine-specific sections this machine takes, see below |
| `plugins` | per-plugin scope, by plugin name |
| `passphrase` / `passphrase_file` | encrypt everything before it leaves; `FFSYNC_PASSPHRASE` beats both |
| `max_file_kb` | drops anything larger, default 1024 |

Give the Steam Deck `"profile": "handheld"` and every desktop `"desktop"`. Same
profile means HUD layout and controls travel too; different profiles means only
the shared set does.

## Running it around the game

### Linux (XIVLauncher.Core)

`~/.local/bin/ffxiv`, then launch that instead of the launcher:

```sh
#!/usr/bin/env sh
# A pull that reports conflicts exits non-zero, and a store that is down should
# not stop anyone playing — so the game starts either way.
ffsync pull || echo "ffsync: pull failed, starting with this machine's settings" >&2

XIVLauncher.Core "$@"

# The launcher exits as soon as the game is up, so waiting on it would push the
# settings from the previous session.
while pgrep -f ffxiv_dx11.exe >/dev/null; do sleep 10; done

ffsync push
```

On Nix or home-manager, package the three steps instead (`writeShellScriptBin`
plus `xdg.desktopEntries`). Make the manual pull and push refuse while
`ffxiv_dx11.exe` runs: the game rewrites its settings at exit, so a pull is
undone and a push uploads the previous session.

### Steam Deck

Same script, saved on the Deck and marked executable. In Steam, add it as a
non-Steam game, or set the launch options of the existing XIVLauncher entry to:

```text
sh -c "$HOME/.local/bin/ffsync pull; %command%; while pgrep -f ffxiv_dx11.exe >/dev/null; do sleep 10; done; $HOME/.local/bin/ffsync push"
```

The flatpak keeps its config under `~/.var/app/dev.goats.xivlauncher`, which
ffsync finds on its own.

### macOS (XIV on Mac)

```sh
#!/usr/bin/env zsh
ffsync pull || echo "ffsync: pull failed, starting with this machine's settings" >&2

open -W -a "XIV on Mac"
while pgrep -f ffxiv_dx11.exe >/dev/null; do sleep 10; done

ffsync push
```

### Windows

`ffxiv.bat`:

```bat
@echo off
setlocal
set FFSYNC=%LOCALAPPDATA%\ffsync\ffsync.exe

"%FFSYNC%" pull || echo ffsync: pull failed, starting anyway

start "" "%LOCALAPPDATA%\XIVLauncher\current\XIVLauncher.exe"

:wait
timeout /t 15 >nul
tasklist /fi "imagename eq XIVLauncher.exe" | find /i "XIVLauncher.exe" >nul && goto wait
tasklist /fi "imagename eq ffxiv_dx11.exe" | find /i "ffxiv_dx11.exe" >nul && goto wait

"%FFSYNC%" push || pause
```

Why each oddity: the launcher sits behind `current\` on Velopack installs; the
loop watches *both* processes, because a slow login or a patch means the game is
not up at the first check; `ffsync.exe` is called by full path, because a
shortcut launched from Explorer may not see a changed `PATH`.

A `.bat` cannot be pinned. Make a shortcut to `cmd.exe /c "…\ffxiv.bat"`, start
minimised, XIVLauncher's icon, pin that.

## What travels

| | |
| --- | --- |
| Shared | hotbars, macros, gearsets, item order, chat log filters, plugin settings |
| Per profile | HUD layout, keybinds, controller bindings, character common settings |
| Merged | `FFXIV.cfg` key by key, and Dalamud's custom repository list |
| Never | resolution, graphics, mouse and gamepad hardware settings, login state, plugin caches, replays, databases and logs, Penumbra's settings, the game's own backups |

`FFXIV.cfg` is one text file holding every kind of setting, so it is merged
rather than copied: the store only receives the keys that travel, and a pull
writes those into the local file without touching the rest.

### Sharing graphics and input settings

Graphics, resolution, mouse and controller settings stay on the machine that
wrote them, because a handheld and a desktop disagree about all of them. Any
section can be moved:

```json
"cfg": {
  "Graphics Settings": "profile",
  "Graphics Settings DX11": "profile",
  "GamePad Settings": "shared",
  "Display Settings/Fps": "shared"
}
```

| Scope | |
| --- | --- |
| `local` | never leaves the machine — the default for everything listed above |
| `profile` | travels between machines of the same shape, so two desktops agree and the Deck keeps its own |
| `shared` | the same everywhere |

`profile` is usually what you want for graphics. A `Section/Key` entry beats a
whole-section one, so a single setting can be pulled out of its section.

Publishing a machine setting and taking one are separate decisions. A machine
only writes an incoming graphics, display or input section if it names it:

```json
"cfg_apply": { "Graphics Settings": true, "Graphics Settings DX11": true }
```

So two desktops with the same hardware can share a graphics preset while a
handheld reading the same store keeps its own, without either of them needing a
profile of its own. Settings that are the player's rather than the machine's —
sound, cutscenes, UI behaviour — need no such entry; they travel as a matter of
course.

## Plugins

A plugin's settings are the JSON beside the others in `pluginConfigs`, or the
JSON inside its own directory. Everything else a plugin keeps there — caches,
replays, databases, logs, lock files — stays on the machine; on this one that is
the difference between 500 KB and 1.2 GB. Anything over `max_file_kb` is skipped
and reported either way.

Plugins are shared by default, with one exception: **Penumbra stays local**. Its
settings are a mod root path and collection ids generated per installation, and
copying them points another machine's Default and Interface collections at ids
it has never seen — which Penumbra reads as "None" and quietly switches every UI
mod off. Name it in `plugins` if you want it anyway.

One whose settings are about the screen rather than the player can be moved the
same way:

```json
"plugins": { "MinimalMeter": "profile", "Browsingway": "local" }
```

Dalamud's custom repository list travels too, merged into
`dalamudConfig.json` by URL — nothing is removed, a repository switched off here
stays off, and the rest of that file is left alone. The plugins themselves are
not synced: with the repositories present, a new machine installs each one from
Dalamud's installer, and its settings are already waiting for it.

Each machine also stores which plugins it has enabled, under its own device
name, so the lists can differ without overwriting each other. They are read
back, never written to a machine:

```sh
ffsync plugins         # what other machines run that this one lacks
ffsync plugins --all   # every plugin, where it is installed, who lists it
```

## Encryption

Plugin settings hold API tokens and push keys, and a store is a folder on
someone else's disk or a server on the internet. With a passphrase set, blobs
are encrypted before they leave:

```sh
export FFSYNC_PASSPHRASE='...'   # or passphrase_file in the settings
```

AES-256-GCM, key derived with PBKDF2-SHA256 once per run. Every machine needs
the same passphrase; a store written without one keeps working.

## Joining a store from a new machine

In this order, or the first sync will fight you:

1. **Start the game once with Dalamud enabled.** Until Dalamud has written
   `dalamudConfig.json` there is nothing to merge repositories into, and
   `ffsync status` will say so rather than claiming the launcher is missing.
2. **Pull before you push.** A fresh Dalamud has no custom repositories, and
   pushing that would be an empty list landing on every other machine. ffsync
   refuses to push an empty repository list for exactly this reason, but the
   rest of a fresh config is still yours to lose.
3. **Expect conflicts on that first pull.** That game start rewrote `FFXIV.cfg`
   and `dalamudConfig.json`, so their local copies are newer than the store's.
   `ffsync pull --force` takes the store's side; what it replaces is kept as
   `<name>.ffsync-bak`.
4. **`ffsync plugins`** lists what other machines run and this one lacks;
   `--all` shows every plugin and which machine lists it. Each machine stores
   its own list, so they can differ without overwriting each other.
   Install those from Dalamud's installer — the repositories they come from
   have already arrived, and their settings are waiting.
5. **Plugins with their own data directories need their own setup.** Penumbra is
   the one to watch: its mod root is machine-specific and deliberately not
   synced, so until you set one, Penumbra and anything built on it do nothing.

## Windows: a trap worth knowing

Packaged (MSIX/Store) applications redirect everything they and their child
processes write under `%AppData%` and `%LOCALAPPDATA%` into a private
per-package folder. Install ffsync from inside one and the binary, config and
wrapper exist for that app and for nothing else — Explorer, PowerShell and the
Start Menu shortcut see none of it. Install to `%USERPROFILE%\ffsync`, or from
an ordinary PowerShell.

Replace the XIVLauncher shortcut you actually use with the one pointing at
`ffxiv.bat`; starting the launcher directly pushes nothing.

## Conflicts

A push whose local copy is older than the store's is reported, not sent; a pull
over a file changed here more recently is reported, not written. `--force` takes
that side instead. `--dry-run` says what would happen. Every file a pull
replaces is kept alongside it as `<name>.ffsync-bak`.

Two machines pushing from the same generation is caught by the store: the second
one is told to pull first.

A push only ever adds or updates. Deleting a file here does not delete it from
the store, so a plugin you removed long ago still has its settings carried
around — harmless, since nothing reads them, but `ffsync plugins` is where you
see which plugins are actually expected.

## Server (optional)

Only needed if the machines do not share a folder.

```sh
docker run -d --name ffsync -p 8771:8771 -v ffsync:/data \
  -e FFSYNC_TOKEN="$(openssl rand -hex 24)" ghcr.io/linusfr/ffxiv-sync:latest
```

Or `deploy/` in this repo: copy `.env.example` to `.env` (or `.envrc.example` to
`.envrc` for direnv), put a token in it, `docker compose up -d`.

| Variable | Default | |
| --- | --- | --- |
| `FFSYNC_TOKEN` | — | required, the bearer token clients send |
| `FFSYNC_DATA` | `/data` | blobs and manifests |
| `FFSYNC_ADDR` | `0.0.0.0:8771` | listen address |
| `FFSYNC_KEEP` | `20` | generations kept before old blobs are collected |

Check it answers, and that the token is doing its job:

```sh
curl -s localhost:8771/healthz                                  # ok
curl -s -o /dev/null -w '%{http_code}\n' localhost:8771/v1/current   # 401
curl -s -H "Authorization: Bearer $FFSYNC_TOKEN" localhost:8771/v1/current
```

Behind a reverse proxy, send the whole host straight through — there is no web
interface, every caller is the CLI, and a CLI cannot follow a login redirect.
`/healthz` is unauthenticated for exactly that reason, so a monitor can watch it.

It stores what the `dir` backend does — content-addressed blobs and a
generation-numbered manifest — so the two cannot drift apart, and the volume is
the entire state: back it up by copying it.

## Keeping it current

```sh
ffsync version --check   # what is running, and the newest release
ffsync update            # replace this binary with it
```

Release assets carry their version in the name, so there is no stable URL to
fetch. `update` picks the build for this platform, checks the downloaded size
against the API's, then renames the old binary aside and the new one in — the
only order Windows allows on a running executable.

A packaged copy is left alone: in the Nix store it is read-only and a rebuild
would undo the replacement, so `update` says where the binary came from and
stops.

## Building

```sh
just check              # fmt, vet, test -race, build, hooks
just release-binaries   # all four platforms into dist/
```

## Licence

MIT.
