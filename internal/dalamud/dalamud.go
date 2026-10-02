// Package dalamud handles dalamudConfig.json, of which only the custom
// repository list travels. The rest of that file is this machine's own state:
// which plugins it has, where its dev plugins live, what it has been shown.
package dalamud

import (
	"encoding/json"
	"fmt"
)

// RepoList is the key Dalamud keeps custom repositories under.
const RepoList = "ThirdRepoList"

// values is how Dalamud's serialiser writes a list: a type tag beside the
// items. It is preserved as-is so Dalamud still recognises the file.
type values struct {
	Type   string            `json:"$type,omitempty"`
	Values []json.RawMessage `json:"$values"`
}

// Repos returns just the repository list, which is what gets uploaded.
func Repos(config []byte) ([]byte, error) {
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(config, &whole); err != nil {
		return nil, fmt.Errorf("reading dalamudConfig.json: %w", err)
	}

	list, ok := whole[RepoList]
	if !ok {
		return nil, nil
	}

	return json.Marshal(map[string]json.RawMessage{RepoList: list})
}

// Merge adds the repositories in remote to the local config, matching on URL.
// Nothing is removed and nothing else in the file is touched: a repository this
// machine switched off stays off, and one it has never seen arrives disabled is
// not something the other machine gets to decide.
func Merge(local, remote []byte) ([]byte, error) {
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(local, &whole); err != nil {
		return nil, fmt.Errorf("reading dalamudConfig.json: %w", err)
	}

	incoming, err := list(remote)
	if err != nil {
		return nil, err
	}
	if len(incoming.Values) == 0 {
		return local, nil
	}

	current, err := list(local)
	if err != nil {
		return nil, err
	}

	have := map[string]bool{}
	for _, raw := range current.Values {
		if url := urlOf(raw); url != "" {
			have[url] = true
		}
	}

	added := false
	for _, raw := range incoming.Values {
		url := urlOf(raw)
		if url == "" || have[url] {
			continue
		}

		current.Values = append(current.Values, raw)
		have[url] = true
		added = true
	}
	if !added {
		return local, nil
	}

	if current.Type == "" {
		current.Type = incoming.Type
	}

	merged, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	whole[RepoList] = merged

	return json.MarshalIndent(whole, "", "  ")
}

// Count reports how many repositories a stored list holds, for the report.
func Count(stored []byte) int {
	parsed, err := list(stored)
	if err != nil {
		return 0
	}

	return len(parsed.Values)
}

func list(config []byte) (values, error) {
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(config, &whole); err != nil {
		return values{}, err
	}

	raw, ok := whole[RepoList]
	if !ok {
		return values{}, nil
	}

	var parsed values
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return values{}, fmt.Errorf("reading %s: %w", RepoList, err)
	}

	return parsed, nil
}

// UrlOf reads a repository's URL, which is what two lists are matched on.
func urlOf(raw json.RawMessage) string {
	var repo struct {
		URL string `json:"Url"`
	}
	if err := json.Unmarshal(raw, &repo); err != nil {
		return ""
	}

	return repo.URL
}
