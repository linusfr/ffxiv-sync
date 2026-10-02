package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/manifest"
)

// HTTP talks to the server, which keeps the same layout a Dir does.
type HTTP struct {
	Base   string
	Token  string
	Client *http.Client
}

// Commit is the body of a push: the manifest, and every blob the server does
// not have yet.
type Commit struct {
	Manifest *manifest.Manifest `json:"manifest"`
	Blobs    map[string][]byte  `json:"blobs"`
}

func (h *HTTP) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}

	return &http.Client{Timeout: 2 * time.Minute}
}

func (h *HTTP) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	address, err := url.JoinPath(h.Base, path)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	if h.Token != "" {
		request.Header.Set("Authorization", "Bearer "+h.Token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	return h.client().Do(request)
}

// Current fetches the newest manifest.
func (h *HTTP) Current(ctx context.Context) (*manifest.Manifest, error) {
	response, err := h.request(ctx, http.MethodGet, "v1/current", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, statusError(response)
	}

	m := manifest.New()
	if err := json.NewDecoder(response.Body).Decode(m); err != nil {
		return nil, err
	}

	return m, nil
}

// Blob fetches one file's contents.
func (h *HTTP) Blob(ctx context.Context, hash string) ([]byte, error) {
	response, err := h.request(ctx, http.MethodGet, "v1/blob/"+hash, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, statusError(response)
	}

	return io.ReadAll(response.Body)
}

// Commit uploads blobs and the manifest in one request, so the server can
// reject a stale push before anything lands.
func (h *HTTP) Commit(ctx context.Context, m *manifest.Manifest, blobs map[string][]byte) error {
	body, err := json.Marshal(Commit{Manifest: m, Blobs: blobs})
	if err != nil {
		return err
	}

	response, err := h.request(ctx, http.MethodPost, "v1/commit", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusConflict:
		return ErrStale
	default:
		return statusError(response)
	}
}

func statusError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return fmt.Errorf("%s: %s", response.Status, bytes.TrimSpace(body))
}
