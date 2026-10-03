package cfg

import (
	"strings"
	"testing"
)

const sample = `<FINAL FANTASY XIV Config File>

<Version>
GuidVersion	538050324
ConfigVersion	2
Language	1

<Network Settings>
UPnP	1
Port	55296
LastLogin0	2478647251

<Display Settings>
ScreenWidth	1280
ScreenHeight	720
Fps	1

<Cutscene Settings>
CutsceneMovieVoice	90
`

func TestSharedDropsMachineSettings(t *testing.T) {
	shared := string(Parse([]byte(sample)).Scoped(Policy{}, Shared).Bytes())

	for _, gone := range []string{"ScreenWidth", "LastLogin0", "Port", "GuidVersion"} {
		if strings.Contains(shared, gone) {
			t.Errorf("shared copy still carries %s", gone)
		}
	}
	for _, kept := range []string{"CutsceneMovieVoice", "Language", "ConfigVersion"} {
		if !strings.Contains(shared, kept) {
			t.Errorf("shared copy lost %s", kept)
		}
	}
}

func TestMergeKeepsLocalMachineSettings(t *testing.T) {
	local := Parse([]byte(sample))

	remote := Parse([]byte(`<FINAL FANTASY XIV Config File>

<Display Settings>
ScreenWidth	3840

<Cutscene Settings>
CutsceneMovieVoice	20

<Sound Settings>
SoundMaster	50
`))

	merged := string(Merge(local, remote, Policy{}).Bytes())

	if !strings.Contains(merged, "ScreenWidth\t1280") {
		t.Error("merge took the remote machine's resolution")
	}
	if !strings.Contains(merged, "CutsceneMovieVoice\t20") {
		t.Error("merge did not take the shared setting")
	}
	if !strings.Contains(merged, "SoundMaster\t50") {
		t.Error("merge dropped a setting the local file had never seen")
	}
}

func TestParseRoundTrip(t *testing.T) {
	if got := string(Parse([]byte(sample)).Bytes()); got != sample {
		t.Errorf("round trip changed the file:\n%s", got)
	}
}

// The game writes CRLF; a round trip that quietly rewrote the whole file as LF
// would show up as every line changing.
func TestCRLFSurvives(t *testing.T) {
	crlf := strings.ReplaceAll(sample, "\n", "\r\n")

	if got := string(Parse([]byte(crlf)).Bytes()); got != crlf {
		t.Error("round trip changed the line endings")
	}
	if got := string(Parse([]byte(sample)).Bytes()); got != sample {
		t.Error("a file without CRLF gained some")
	}
}

func TestGraphicsStayLocalByDefault(t *testing.T) {
	policy := Policy{}

	for _, section := range []string{"Graphics Settings", "Graphics Settings DX11", "GamePad Settings", "Mouse Settings"} {
		if policy.Scope(section, "anything") != Local {
			t.Errorf("%s would travel between machines", section)
		}
	}
	if policy.Scope("Sound Settings", "SoundMaster") != Shared {
		t.Error("sound is a player preference, not a machine one")
	}
}

// Graphics can be opted in, which is the point of the override.
func TestGraphicsCanBeShared(t *testing.T) {
	policy := Policy{Overrides: map[string]Scope{
		"Graphics Settings":              Profiled,
		"Display Settings/Fps":           Shared,
		"Graphics Settings/GrassQuality": Local,
	}}

	cases := []struct {
		section, key string
		want         Scope
	}{
		{"Graphics Settings", "SSAO", Profiled},
		{"Graphics Settings", "GrassQuality", Local}, // the key beats the section
		{"Display Settings", "Fps", Shared},
		{"Display Settings", "ScreenWidth", Local},
	}

	for _, c := range cases {
		if got := policy.Scope(c.section, c.key); got != c.want {
			t.Errorf("Scope(%s/%s) = %s, want %s", c.section, c.key, got, c.want)
		}
	}
}

func TestScopedSplitsTheFile(t *testing.T) {
	policy := Policy{Overrides: map[string]Scope{"Display Settings": Profiled}}
	parsed := Parse([]byte(sample))

	profiled := string(parsed.Scoped(policy, Profiled).Bytes())
	if !strings.Contains(profiled, "ScreenWidth") {
		t.Error("the profiled copy lost the resolution it was asked to carry")
	}
	if strings.Contains(profiled, "CutsceneMovieVoice") {
		t.Error("the profiled copy picked up a shared setting")
	}

	shared := string(parsed.Scoped(policy, Shared).Bytes())
	if strings.Contains(shared, "ScreenWidth") {
		t.Error("the shared copy still carries the resolution")
	}
	if !parsed.Travels(policy, Profiled) || !parsed.Travels(policy, Shared) {
		t.Error("Travels disagrees with Scoped")
	}
	if (Policy{}).Scope("Display Settings", "ScreenWidth") != Local {
		t.Error("the default changed")
	}
}

// Publishing a machine setting and taking one are separate decisions.
func TestApplyingNeedsTheReceiversConsent(t *testing.T) {
	published := Policy{Overrides: map[string]Scope{"Graphics Settings": Profiled}}

	if published.Applies("Graphics Settings", "SSAO") {
		t.Error("a published graphics setting was applied without being asked for")
	}
	if !published.Applies("Sound Settings", "SoundMaster") {
		t.Error("an ordinary player setting needs asking for, which it should not")
	}

	accepting := Policy{
		Overrides: published.Overrides,
		Accept:    map[string]bool{"Graphics Settings": true},
	}
	if !accepting.Applies("Graphics Settings", "SSAO") {
		t.Error("the receiver asked for graphics and did not get them")
	}

	// Accepting a section this machine keeps local changes nothing: local wins.
	stubborn := Policy{Accept: map[string]bool{"Display Settings": true}}
	if stubborn.Applies("Display Settings", "ScreenWidth") {
		t.Error("resolution was applied to a machine that keeps it local")
	}
}
