// Package cfg reads and writes FFXIV.cfg, the game's one text config file. It
// holds settings worth sharing between machines next to settings that describe
// the machine itself, so it is the only file merged key by key.
package cfg

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Line is one line of the file, kept in order so a rewrite looks like the
// game's own output rather than a reformatting.
type Line struct {
	Section string
	Key     string
	Value   string
	Raw     string // blank lines, the header and section markers
}

// File is FFXIV.cfg in the order the game wrote it.
type File struct {
	Lines []Line

	// CRLF records how the game ends its lines, so a rewrite does not quietly
	// convert the whole file.
	CRLF bool
}

// Scope says how far a setting travels.
type Scope int

const (
	// Local never leaves the machine that wrote it.
	Local Scope = iota

	// Shared is the same on every machine.
	Shared

	// Profiled travels only between machines of the same shape, which is what
	// graphics settings want: two desktops can agree, a handheld cannot.
	Profiled
)

// ParseScope reads a scope from the settings file.
func ParseScope(name string) (Scope, bool) {
	switch strings.ToLower(name) {
	case "local":
		return Local, true
	case "shared":
		return Shared, true
	case "profile", "profiled":
		return Profiled, true
	default:
		return Local, false
	}
}

func (s Scope) String() string {
	switch s {
	case Shared:
		return "shared"
	case Profiled:
		return "profile"
	default:
		return "local"
	}
}

// defaults are the sections that describe the machine rather than the player.
// A section mapped to nil is local in full; everything not listed is shared.
var defaults = map[string][]string{
	// Resolution, window position, adapter, frame limit.
	"Display Settings": nil,

	// What this GPU can afford, which is the whole point of having a handheld
	// and a desktop.
	"Graphics Settings":      nil,
	"Graphics Settings DX11": nil,

	// Input hardware differs per machine.
	"Mouse Settings":          nil,
	"GamePad Settings":        nil,
	"GamePad Button Settings": nil,

	// Session state, not settings.
	"Network Settings": nil,

	// Written by the game per installation.
	"Version": {"GuidVersion"},
}

// Policy decides each setting's scope. The zero value is the default: machine
// settings stay put. Overrides are keyed by section, or by "Section/Key" for a
// single setting, and are how graphics are opted into sharing.
type Policy struct {
	Overrides map[string]Scope
}

// Scope reports how far one setting travels.
func (p Policy) Scope(section, key string) Scope {
	if scope, ok := p.Overrides[section+"/"+key]; ok {
		return scope
	}
	if scope, ok := p.Overrides[section]; ok {
		return scope
	}

	keys, listed := defaults[section]
	if !listed {
		return Shared
	}
	if keys == nil {
		return Local
	}

	for _, k := range keys {
		if k == key {
			return Local
		}
	}

	return Shared
}

// Sections lists what can be overridden, for error messages and documentation.
func Sections() []string {
	out := make([]string, 0, len(defaults))
	for section := range defaults {
		out = append(out, section)
	}
	sort.Strings(out)

	return out
}

// Parse reads FFXIV.cfg. Unknown lines are preserved verbatim.
func Parse(data []byte) *File {
	file := &File{}
	section := ""

	file.CRLF = bytes.Contains(data, []byte("\r\n"))

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") {
			section = strings.Trim(trimmed, "<>")
			file.Lines = append(file.Lines, Line{Raw: line})
			continue
		}

		key, value, ok := strings.Cut(line, "\t")
		if !ok || trimmed == "" {
			file.Lines = append(file.Lines, Line{Raw: line})
			continue
		}

		file.Lines = append(file.Lines, Line{Section: section, Key: key, Value: value})
	}

	return file
}

// Bytes writes the file back out, keeping the game's line endings.
func (f *File) Bytes() []byte {
	newline := "\n"
	if f.CRLF {
		newline = "\r\n"
	}

	var out bytes.Buffer
	for _, line := range f.Lines {
		if line.Key == "" {
			fmt.Fprint(&out, line.Raw, newline)
			continue
		}

		fmt.Fprint(&out, line.Key, "\t", line.Value, newline)
	}

	return out.Bytes()
}

// Scoped returns the part of the file that travels at one scope: the shared
// copy for every machine, or the profiled copy for machines of this shape. What
// is left out never leaves, so the store never holds another machine's
// resolution unless it was asked to.
func (f *File) Scoped(policy Policy, want Scope) *File {
	out := &File{CRLF: f.CRLF}

	for _, line := range f.Lines {
		if line.Key != "" && policy.Scope(line.Section, line.Key) != want {
			continue
		}

		out.Lines = append(out.Lines, line)
	}

	return out
}

// Travels reports whether any setting is stored at this scope, so a scope with
// nothing in it is not uploaded as an empty file.
func (f *File) Travels(policy Policy, want Scope) bool {
	for _, line := range f.Lines {
		if line.Key != "" && policy.Scope(line.Section, line.Key) == want {
			return true
		}
	}

	return false
}

// Merge applies the settings in remote to a local file, leaving anything local
// untouched. Settings the local file has never seen are appended to their
// section. Remote may be a shared or a profiled copy; the other scope's
// settings are simply not in it.
func Merge(local, remote *File, policy Policy) *File {
	incoming := map[string]string{}
	order := []string{}

	for _, line := range remote.Lines {
		if line.Key == "" || policy.Scope(line.Section, line.Key) == Local {
			continue
		}

		id := line.Section + "\x00" + line.Key
		if _, seen := incoming[id]; !seen {
			order = append(order, id)
		}
		incoming[id] = line.Value
	}

	merged := &File{CRLF: local.CRLF}
	for _, line := range local.Lines {
		if line.Key != "" {
			id := line.Section + "\x00" + line.Key
			if value, ok := incoming[id]; ok {
				line.Value = value
				delete(incoming, id)
			}
		}

		merged.Lines = append(merged.Lines, line)
	}

	// Whatever the local file did not have yet is added at the end of its
	// section, in the order the remote file listed it.
	for _, id := range order {
		value, ok := incoming[id]
		if !ok {
			continue
		}

		section, key, _ := strings.Cut(id, "\x00")
		merged.insert(section, key, value)
	}

	return merged
}

// Insert puts a setting at the end of its section, creating the section if the
// local file does not have it.
func (f *File) insert(section, key, value string) {
	last := -1
	for i, line := range f.Lines {
		if line.Section == section {
			last = i
		}
	}

	line := Line{Section: section, Key: key, Value: value}
	if last < 0 {
		f.Lines = append(f.Lines, Line{Raw: ""}, Line{Raw: "<" + section + ">"}, line)
		return
	}

	f.Lines = append(f.Lines[:last+1], append([]Line{line}, f.Lines[last+1:]...)...)
}
