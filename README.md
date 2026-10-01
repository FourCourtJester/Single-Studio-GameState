# Single Studio Companion

A small local relay for game state feeds that a browser cannot read directly.
It acquires each game's data however that game requires, then pushes the raw
payloads to Single Studio over one local WebSocket. All shaping happens in
Single Studio; the companion does no diffing or transforming.

The companion is optional and separate from Single Studio. You only need it
for the titles below.

## Supported titles

| Title             | Namespace | How the companion gets the data                        | Status          |
| ----------------- | --------- | ------------------------------------------------------ | --------------- |
| Apex Legends      | `apex`    | Hosts a WebSocket server the game connects to (LiveAPI) | Implemented     |
| StarCraft II      | `sc2`     | Polls the client API `/game` and `/ui` on localhost:6119 | Implemented     |
| League of Legends | `lol`     | Polls live client data on https://127.0.0.1:2999        | Implemented     |
| Counter-Strike 2  | `cs2`     | Receives Game State Integration POSTs                   | Implemented     |
| Dota 2            | `dota2`   | Receives Game State Integration POSTs                   | Implemented     |
| Warcraft III      | `war3`    | Transport still to be confirmed                         | Not implemented |

## Usage

Double-click the companion. It opens its control panel in your browser:

![Control panel](docs/panel.png)

- **Game:** pick the title you're streaming. The choice is remembered.
- **On/off:** start or stop relaying. Switching games while on swaps over
  straight away; the old game's data stays in Single Studio.
- **Errors:** appears only when something goes wrong (a port already in use,
  a rejected GSI token) and disappears when cleared.

The panel is dark by default; the button in its corner switches to light, and
the choice is remembered.

Closing the browser tab leaves the companion running; launching it again
reopens the panel. Close the companion's console window to quit.

Single Studio connects to `ws://127.0.0.1:47600/ws`. `GET /status` reports the
game, whether it is on, the connected overlay count and when the last payload
arrived.

For headless use, flags skip the panel:

```sh
companion -no-browser -game sc2                 # relay StarCraft II immediately
companion -no-browser -game sc2 -interval 250ms # poll at 4 Hz
companion -config companion.json                # read settings from a file; flags still win
```

### Per-game setup

- **CS2 / Dota 2:** generate the GSI config and save it where the first line
  says:

  ```sh
  companion gsi-config -game cs2 > gamestate_integration_singlestudio.cfg
  ```

  Add `-gsi-token <secret>` to both commands to require a token. The companion
  strips the token before relaying.
- **Apex:** add these launch options:
  `+cl_liveapi_enabled 1 +cl_liveapi_ws_servers "ws://127.0.0.1:7777"`.
  JSON payloads are relayed as-is. Protobuf payloads are relayed base64-encoded.
- **StarCraft II and League:** no setup. Start a game or replay and the
  companion picks it up.

### Settings

| Flag         | JSON key         | Default                 | Used by    |
| ------------ | ---------------- | ----------------------- | ---------- |
| `-game`      | `game`           | none (pick in panel)    | all        |
| `-bind`      | `bind`           | `127.0.0.1`             | all        |
| `-port`      | `port`           | `47600`                 | relay      |
| `-interval`  | `interval`       | `1s`                    | sc2, lol   |
| `-gsi-port`  | `gsiPort`        | `47601`                 | cs2, dota2 |
| `-gsi-token` | `gsiToken`       | none                    | cs2, dota2 |
| `-apex-port` | `apexPort`       | `7777`                  | apex       |
| `-sc2-url`   | `sc2Url`         | `http://127.0.0.1:6119` | sc2        |
| (none)       | `allowedOrigins` | `["*"]`                 | relay      |

Every listener binds to 127.0.0.1 by default, so nothing is reachable from
the network. The default ports are provisional.

## Wire format

Each WebSocket message is one JSON envelope:

```json
{ "ns": "sc2", "ts": 1790822970435, "data": { "game": { ... }, "ui": { ... } } }
```

- `ns` is the game namespace. Single Studio saves `data` under it.
- `ts` is the time the companion received the payload, in Unix milliseconds.
- `data` is the payload verbatim when it is JSON. Otherwise it is a base64
  string and `"encoding": "base64"` is set.

Full payloads are sent on every tick, and Yjs only emits updates for keys
that changed. A newly connected client immediately receives the latest
payload.

## Development

```sh
go test -race ./...
GOOS=windows GOARCH=amd64 go build -o dist/companion.exe ./cmd/companion
GOOS=darwin  GOARCH=arm64 go build -o dist/companion-mac ./cmd/companion
```

Layout:

- `cmd/companion`: flags, the relay server and the `gsi-config` command
- `internal/relay`: the envelope and the WebSocket fan-out hub
- `internal/adapter`: per-title acquisition (poll, receive HTTP, host a WebSocket)
- `internal/gsi`: CS2 / Dota 2 GSI config generation
- `internal/config`: settings, defaults, validation and the remembered game
- `internal/control`: starts, stops and switches the adapter; collects errors
- `internal/ui`: the control panel page and its JSON API
