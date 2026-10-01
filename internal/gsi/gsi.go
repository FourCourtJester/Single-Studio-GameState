// Package gsi generates the Game State Integration config files that point
// CS2 and Dota 2 at the companion's receiver.
package gsi

import (
	"fmt"
	"strings"
)

// FileName is the file name the game looks for. Any name matching
// gamestate_integration_*.cfg is picked up.
const FileName = "gamestate_integration_singlestudio.cfg"

// Install locations, relative to the game's install directory.
var InstallDir = map[string]string{
	"cs2":   `game/csgo/cfg`,
	"dota2": `game/dota/cfg/gamestate_integration`,
}

// Data sections requested from each game. CS2's allplayers_* sections are
// only populated for observers and GOTV, which is the overlay use case.
var sections = map[string][]string{
	"cs2": {
		"provider", "map", "round", "phase_countdowns", "map_round_wins",
		"bomb", "allgrenades",
		"player_id", "player_state", "player_weapons", "player_match_stats", "player_position",
		"allplayers_id", "allplayers_state", "allplayers_weapons", "allplayers_match_stats", "allplayers_position",
	},
	"dota2": {
		"provider", "map", "player", "hero", "abilities", "items",
		"buildings", "draft", "wearables",
	},
}

// Config returns the contents of the GSI config file for game, pointing at
// uri. When token is non-empty it is sent with every payload.
func Config(game, uri, token string) (string, error) {
	data, ok := sections[game]
	if !ok {
		return "", fmt.Errorf("%q does not use game state integration", game)
	}

	var b strings.Builder
	b.WriteString("\"Single Studio Companion\"\n{\n")
	fmt.Fprintf(&b, "\t\"uri\"\t\t%q\n", uri)
	b.WriteString("\t\"timeout\"\t\"5.0\"\n")
	b.WriteString("\t\"buffer\"\t\"0.1\"\n")
	b.WriteString("\t\"throttle\"\t\"0.1\"\n")
	b.WriteString("\t\"heartbeat\"\t\"30.0\"\n")
	if token != "" {
		fmt.Fprintf(&b, "\t\"auth\"\n\t{\n\t\t\"token\"\t%q\n\t}\n", token)
	}
	b.WriteString("\t\"data\"\n\t{\n")
	for _, s := range data {
		fmt.Fprintf(&b, "\t\t%q\t\"1\"\n", s)
	}
	b.WriteString("\t}\n}\n")
	return b.String(), nil
}
