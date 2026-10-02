// Package store keeps manifests and blobs. A local directory and the server use
// the same layout, so a folder replicated by Syncthing and a homelab server are
// the same store reached two ways.
package store

import (
	"context"
	"errors"

	"github.com/linusfr/ffxiv-sync/internal/manifest"
)

// ErrStale means someone else committed first and this client has to pull.
var ErrStale = errors.New("store moved on; pull first")

// Store is everything a client needs.
type Store interface {
	// Current returns the newest manifest, or an empty one for a fresh store.
	Current(ctx context.Context) (*manifest.Manifest, error)

	// Blob returns file contents by hash.
	Blob(ctx context.Context, hash string) ([]byte, error)

	// Commit writes blobs and then the manifest, and fails with ErrStale unless
	// the manifest's generation is exactly one past what the store holds.
	Commit(ctx context.Context, m *manifest.Manifest, blobs map[string][]byte) error
}
