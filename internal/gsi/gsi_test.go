package gsi

import (
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	out, err := Config("cs2", "http://127.0.0.1:47601/", "tok")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"uri"		"http://127.0.0.1:47601/"`,
		`"token"	"tok"`,
		`"allplayers_state"	"1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "{") != strings.Count(out, "}") {
		t.Errorf("unbalanced braces:\n%s", out)
	}
}

func TestConfigOmitsEmptyToken(t *testing.T) {
	out, _ := Config("dota2", "http://127.0.0.1:47601/", "")
	if strings.Contains(out, "auth") {
		t.Errorf("unexpected auth block:\n%s", out)
	}
}

func TestConfigRejectsNonGSIGames(t *testing.T) {
	if _, err := Config("sc2", "http://x/", ""); err == nil {
		t.Fatal("expected an error for sc2")
	}
}
