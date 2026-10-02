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
