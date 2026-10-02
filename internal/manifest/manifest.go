// Package manifest describes what the store holds: one entry per file, keyed by
// where it belongs rather than where it came from.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/policy"
)

// Version is the manifest format, so an older client can refuse a newer store
// instead of mangling it.
const Version = 1

// Entry is one stored file. Hash is of the file's contents and Blob of what
// the store actually holds, which differ once a store is encrypted: the hash
// still tells two machines whether a file changed, and the blob is what gets
// fetched.
type Entry struct {
	Hash       string    `json:"hash"`
	Blob       string    `json:"blob,omitempty"`
	Encrypted  bool      `json:"encrypted,omitempty"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Device     string    `json:"device"`
	Generation int64     `json:"generation"`
}

// Manifest is the whole store at one point in time. Generation increases by one
// per commit and is what keeps two machines from overwriting each other.
type Manifest struct {
	Version    int              `json:"version"`
	Generation int64            `json:"generation"`
	Device     string           `json:"device"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Entries    map[string]Entry `json:"entries"`
}

// New returns an empty manifest, which is also what an untouched store holds.
func New() *Manifest {
	return &Manifest{Version: Version, Entries: map[string]Entry{}}
}

// Key is where a file belongs in the store. Profile files are kept apart so a
// handheld and a desktop can both have one without ever seeing the other's.
func Key(logical string, action policy.Action, profile string) string {
	if action == policy.Profile {
		return "profiles/" + profile + "/" + logical
	}

	return "shared/" + logical
}

// Stored is the blob to fetch for an entry.
func (e Entry) Stored() string {
	if e.Blob != "" {
		return e.Blob
	}

	return e.Hash
}

// Mine reports whether an entry belongs to this machine's profile. Shared
// entries belong to everyone.
func Mine(key, profile string) bool {
	if logical, ok := CutShared(key); ok {
		return logical != ""
	}

	_, ok := CutProfile(key, profile)
	return ok
}

// CutShared returns the logical path of a shared entry.
func CutShared(key string) (string, bool) {
	return cut(key, "shared/")
}

// CutProfile returns the logical path of an entry belonging to one profile.
func CutProfile(key, profile string) (string, bool) {
	return cut(key, "profiles/"+profile+"/")
}

func cut(key, prefix string) (string, bool) {
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return "", false
	}

	return key[len(prefix):], true
}

// Hash is how blobs are addressed: the same bytes are stored once, however many
// machines and generations refer to them.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
