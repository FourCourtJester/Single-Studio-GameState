# Single Studio - GameState

A small local relay for game state feeds that a browser cannot read directly.
It acquires each game's data however that game requires, then pushes the raw
payloads to Single Studio over one local WebSocket. All shaping happens in
Single Studio; GameState does no diffing or transforming.

GameState is optional and separate from Single Studio. You only need it
for the titles below.

## Supported titles

| Title             | ID        | How GameState gets the data                          | Default port | Status          |
| ----------------- | --------- | ---------------------------------------------------- | ------------ | --------------- |
| Apex Legends      | `apex`    | Hosts a WebSocket server the game connects to (LiveAPI) | 7777      | Implemented     |
| Counter-Strike 2  | `cs2`     | Receives Game State Integration POSTs                | 47601        | Implemented     |
| Dota 2            | `dota2`   | Receives Game State Integration POSTs                | 47601        | Implemented     |
| League of Legends | `lol`     | Polls live client data on https://127.0.0.1:2999     | fixed (2999) | Implemented     |
| Rocket League     | `rl`      | Connects to the Stats API WebSocket the game serves  | 49124        | Implemented     |
| StarCraft II      | `sc2`     | Polls the client API `/game` and `/ui`               | 6119         | Implemented     |
| Warcraft III      | `war3`    | Transport still to be confirmed                      |              | Not implemented |

Every port except League's can be changed in the window to match your own
setup.

## Download

Grab the build for your system from the
[Releases](https://github.com/fourcourtjester/single-studio-gamestate/releases) page:

| System                | File                                        | To run                                   |
| --------------------- | ------------------------------------------- | ---------------------------------------- |
| Windows               | `Single-Studio-GameState-windows-amd64.exe`   | Double-click it                          |
| Mac (Apple Silicon)   | `Single-Studio-GameState-macos-arm64.zip`     | Unzip, then open the app                 |
| Mac (Intel)           | `Single-Studio-GameState-macos-intel.zip`     | Unzip, then open the app                 |
| Linux                 | `Single-Studio-GameState-linux-amd64.tar.xz`  | Extract and run `usr/local/bin/gamestate`, or `make user-install` for a menu entry |

The builds are not code-signed yet. On Windows, SmartScreen may warn on first
launch ("More info" → "Run anyway"). On a Mac, right-click the app and choose
**Open** the first time.

## Usage

Opening GameState shows its window:

![GameState window](docs/panel.png)

- **Game:** pick the title you're streaming; each has a coloured badge. The
  choice is remembered.
- **Game port:** for games whose port can change, the port GameState uses
  to reach that game, prefilled with its default. If you've changed the
  port in the game's own setup, enter the same one and press Apply. Each
  game remembers its own port; changing it while that game is on reconnects
  straight away.
- **On/off:** start or stop relaying. Switching games while on swaps over
  straight away; the old game's data stays in Single Studio.
- **Broadcast port:** the port Single Studio connects to (47600 by default).
  Change it and press Apply to move; connected overlays are disconnected and
  must reconnect on the new port. If the new port is busy, GameState stays on
  the old one and says why. The choice is remembered. If the port is taken
  when GameState starts, the game controls stay off until you pick a free one.
- **Errors:** appears only when something goes wrong (a port already in use,
  a payload that isn't JSON). The window grows to fit it and shrinks back when
  cleared, unless you've resized the window yourself.

The window is dark by default; the button in its corner switches to light, and
the choice is remembered. GameState runs for as long as the window is
open: minimise it while you stream, close it to quit. Opening GameState
again while it's running brings the existing window forward.

Single Studio connects to `ws://127.0.0.1:47600/ws` (`ws://127.0.0.1:47600` works too). `GET /status` reports the
game, whether it is on, the connected overlay count and when the last payload
arrived.

For headless use (a server, or scripting), `-no-window` runs without a window:

```sh
gamestate -no-window -game sc2                 # relay StarCraft II immediately
gamestate -no-window -game sc2 -interval 250ms # poll at 4 Hz
gamestate -no-window -config gamestate.json    # read settings from a file; flags still win
```

### Per-game setup

The ports below are the defaults; use whatever your setup uses and set the
same port in the window's **Game port** box. The window shows the address
to use for the selected game, with a link to that game's own guide.

- **CS2 / Dota 2:** the game only sends its state to the addresses listed in
  your Game State Integration config file. Creating and managing that file
  is up to you; its `uri` must point at GameState
  (`http://127.0.0.1:47601/` by default). Payloads are relayed exactly as the
  game sends them. Valve's
  [Game State Integration guide](https://developer.valvesoftware.com/wiki/Counter-Strike:_Global_Offensive_Game_State_Integration)
  covers the file for both games.
- **Apex:** add these launch options:
  `+cl_liveapi_enabled 1 +cl_liveapi_ws_servers "ws://127.0.0.1:7777"`.
  JSON is relayed as text, protobuf as binary, both unchanged.
- **Rocket League:** turn on the game's
  [Stats API](https://www.rocketleague.com/en/developer/stats-api) by setting
  `PacketSendRate` in `DefaultStatsAPI.ini` (that file is yours to manage).
  GameState connects to its WebSocket (`WebPort`, 49124 by default) and
  relays each event (`{"Event": ..., "Data": ...}`) exactly as the game sends
  it; `Data` is a JSON-encoded string. Rocket League can also be read from a
  browser directly; it's here so every title works the same way.
- **StarCraft II:** no setup. GameState polls the client API on port 6119;
  if you start the game with `-clientapi` on another port, match it.
- **League:** no setup. Start a game or replay and GameState picks it up.

### Settings

| Flag         | JSON key         | Default                 | Used by    |
| ------------ | ---------------- | ----------------------- | ---------- |
| `-game`      | `game`           | none (pick in window)   | all        |
| `-bind`      | `bind`           | `127.0.0.1`             | all        |
| `-port`      | `port`           | `47600` (or the port set in the window) | relay |
| `-interval`  | `interval`       | `1s`                    | sc2, lol   |
| `-game-port` | `gamePorts`      | each title's default, or the port set in the window | the `-game` game; `gamePorts` maps game ID to port, e.g. `{"rl": 49125}` |
| (none)       | `allowedOrigins` | `["*"]`                 | relay      |

Every listener binds to 127.0.0.1 by default, so nothing is reachable from
the network. The default ports are provisional.

## Wire format

GameState is a transparent pass-through. Every message the active game sends
goes out on the broadcast port exactly as the game sent it, with nothing
added, wrapped or changed:

- Text messages (JSON from every game) are sent as WebSocket text frames.
- Anything else (Apex LiveAPI in protobuf mode) is sent as binary frames.

One game runs at a time, so everything on the port belongs to the game
selected in the window. A newly connected client immediately receives the
latest message; switching games clears it, so a client never gets the
previous game's data.

The one addition is for games read over more than one address. StarCraft II's
client API answers on two (`/game` and `/ui`); each is polled and sent as its
own message, with one field added at the root so Single Studio can tell the
two apart whatever their shape:

```json
{"_ssg": "game", ...the game's /game response...}
{"_ssg": "ui", ...the game's /ui response...}
```

Every other byte is the game's own. Games with a single address get nothing
added.

Full payloads are sent on every tick, and Yjs only emits updates for keys
that changed.

## Development

The window uses [Fyne](https://fyne.io), which needs a C compiler and, on
Linux, the OpenGL and X11/Wayland headers:

```sh
sudo apt-get install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev wayland-protocols
go test -race ./...
go run ./cmd/gamestate
```

Each OS is built on its own machine. CI does this for every push, and pushing
a `v*` tag publishes the builds as a GitHub Release. To package locally:

```sh
go install fyne.io/tools/cmd/fyne@v1.7.3
cd cmd/gamestate && fyne package --target linux --release   # or windows / darwin on those systems
```

`PANEL_SHOTS=<dir> go test -run Screenshots ./internal/ui` renders the window
in each state to PNGs, for checking visual changes.

Layout:

- `cmd/gamestate`: flags, the relay server and the window
- `internal/relay`: the pass-through WebSocket fan-out hub
- `internal/adapter`: per-title acquisition (poll, receive HTTP, host a WebSocket)
- `internal/config`: settings, defaults, validation and remembered choices
- `internal/control`: starts, stops and switches the adapter; collects errors
- `internal/ui`: the window's panel, its on/off switch and theme
