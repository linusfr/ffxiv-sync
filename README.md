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
```

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
| `cfg` | moves `FFXIV.cfg` sections between scopes, see below |
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
set -e
ffsync pull
XIVLauncher.Core
# The launcher exits before the game does, so wait for the game itself.
while pgrep -f ffxiv_dx11.exe >/dev/null; do sleep 10; done
ffsync push
```

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
set -e
ffsync pull
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

Three things that bite here. The launcher is behind `current\` on Velopack
installs. The loop has to watch *both* processes, or a slow login, an OTP prompt
or a patch means the game is not up at the first check and the push happens
before the session. And `ffsync.exe` is called by full path, because a shortcut
launched from Explorer may not see a freshly changed `PATH`.

A `.bat` cannot be pinned to the taskbar. Make a shortcut with target
`cmd.exe /c "…\ffxiv.bat"`, set it to start minimised, give it XIVLauncher's
icon, and pin that from the Start Menu.

## What travels

| | |
| --- | --- |
| Shared | hotbars, macros, gearsets, item order, chat log filters, plugin settings |
| Per profile | HUD layout, keybinds, controller bindings, character common settings |
| Merged | `FFXIV.cfg` key by key, and Dalamud's custom repository list |
| Never | resolution, graphics, mouse and gamepad hardware settings, login state, plugin caches, replays, databases and logs, the game's own backups |

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

## Plugins

A plugin's settings are the JSON beside the others in `pluginConfigs`, or the
JSON inside its own directory. Everything else a plugin keeps there — caches,
replays, databases, logs, lock files — stays on the machine; on this one that is
the difference between 500 KB and 1.2 GB. Anything over `max_file_kb` is skipped
and reported either way.

Plugins are shared by default. One whose settings are about the screen rather
than the player can be moved:

```json
"plugins": { "MinimalMeter": "profile", "Browsingway": "local" }
```

Dalamud's custom repository list travels too, merged into
`dalamudConfig.json` by URL — nothing is removed, a repository switched off here
stays off, and the rest of that file is left alone. The plugins themselves are
not synced: with the repositories present, a new machine installs each one from
Dalamud's installer, and its settings are already waiting for it.

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
4. **`ffsync plugins`** lists what the store expects and this machine lacks.
   Install those from Dalamud's installer — the repositories they come from
   have already arrived, and their settings are waiting.
5. **Plugins with their own data directories need their own setup.** Penumbra is
   the one to watch: its mod root is machine-specific and deliberately not
   synced, so until you set one, Penumbra and anything built on it do nothing.

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
docker run -d -p 8771:8771 -v ffsync:/data \
  -e FFSYNC_TOKEN=... ghcr.io/linusfr/ffxiv-sync:latest
```

| Variable | Default | |
| --- | --- | --- |
| `FFSYNC_TOKEN` | — | required, the bearer token clients send |
| `FFSYNC_DATA` | `/data` | blobs and manifests |
| `FFSYNC_ADDR` | `0.0.0.0:8771` | listen address |
| `FFSYNC_KEEP` | `20` | generations kept before old blobs are collected |

It stores exactly what the `dir` backend does — content-addressed blobs and a
generation-numbered manifest — so the two cannot drift apart.

## Building

```sh
just check              # fmt, vet, test -race, build, hooks
just release-binaries   # all four platforms into dist/
```

## Licence

MIT.
