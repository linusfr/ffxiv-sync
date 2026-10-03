package dalamud

import (
	"encoding/json"
	"strings"
	"testing"
)

const config = `{
  "$type": "Dalamud.Configuration.Internal.DalamudConfiguration, Dalamud",
  "DoPluginTest": false,
  "ThirdRepoList": {
    "$type": "System.Collections.Generic.List, System.Private.CoreLib",
    "$values": [
      { "Url": "https://example.com/one.json", "IsEnabled": true },
      { "Url": "https://example.com/two.json", "IsEnabled": false }
    ]
  },
  "DevPluginLoadLocations": { "$values": [] }
}`

func TestReposIsOnlyTheList(t *testing.T) {
	repos, err := Repos([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(repos), "DevPluginLoadLocations") {
		t.Error("the upload carries machine state it has no business carrying")
	}
	if Count(repos) != 2 {
		t.Errorf("stored %d repositories, want 2", Count(repos))
	}
}

func TestMergeAddsWithoutTouchingTheRest(t *testing.T) {
	remote := []byte(`{"ThirdRepoList":{"$values":[
		{"Url":"https://example.com/two.json","IsEnabled":true},
		{"Url":"https://example.com/three.json","IsEnabled":true}]}}`)

	merged, err := Merge([]byte(config), remote)
	if err != nil {
		t.Fatal(err)
	}

	var whole map[string]json.RawMessage
	if err := json.Unmarshal(merged, &whole); err != nil {
		t.Fatal(err)
	}
	if _, ok := whole["DevPluginLoadLocations"]; !ok {
		t.Error("merge dropped the rest of the file")
	}
	if Count(merged) != 3 {
		t.Errorf("%d repositories after merge, want 3", Count(merged))
	}

	// The local copy said two.json is off. The other machine does not get to
	// turn it back on.
	parsed, _ := list(merged)
	for _, raw := range parsed.Values {
		var repo struct {
			URL       string `json:"Url"`
			IsEnabled bool   `json:"IsEnabled"`
		}
		json.Unmarshal(raw, &repo)
		if repo.URL == "https://example.com/two.json" && repo.IsEnabled {
			t.Error("a repository this machine disabled was switched back on")
		}
	}
}

func TestMergeWithNothingNewLeavesTheFileAlone(t *testing.T) {
	remote := []byte(`{"ThirdRepoList":{"$values":[{"Url":"https://example.com/one.json","IsEnabled":true}]}}`)

	merged, err := Merge([]byte(config), remote)
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != config {
		t.Error("a merge that changes nothing rewrote the file anyway")
	}
}

func TestPluginsDropsPerMachineIds(t *testing.T) {
	config := []byte(`{"DefaultProfile":{"Plugins":{"$values":[
		{"InternalName":"BossMod","WorkingPluginId":"64c8cdba-e9ce-4a0e-aae0-bdff6106c810","IsEnabled":true},
		{"InternalName":"AutoRetainer","WorkingPluginId":"other","IsEnabled":false}]}}}`)

	plugins, err := Plugins(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}
	// Sorted, so a push does not churn the blob when Dalamud reorders them.
	if plugins[0].InternalName != "AutoRetainer" || plugins[1].InternalName != "BossMod" {
		t.Errorf("not sorted: %+v", plugins)
	}
	if plugins[1].IsEnabled != true || plugins[0].IsEnabled != false {
		t.Errorf("enabled state lost: %+v", plugins)
	}

	// WorkingPluginId is generated per machine; carrying it would say nothing
	// true about the machine reading it.
	stored, _ := json.Marshal(plugins)
	if strings.Contains(string(stored), "64c8cdba") {
		t.Error("per-machine plugin id travelled")
	}
}

// A repository URL with stray whitespace is the same repository.
func TestMergeIgnoresWhitespaceInUrls(t *testing.T) {
	remote := []byte(`{"ThirdRepoList":{"$values":[{"Url":" https://example.com/one.json","IsEnabled":true}]}}`)

	merged, err := Merge([]byte(config), remote)
	if err != nil {
		t.Fatal(err)
	}
	if Count(merged) != 2 {
		t.Errorf("%d repositories, want 2 — a padded URL was added again", Count(merged))
	}
}
