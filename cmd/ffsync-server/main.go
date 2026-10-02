// Command ffsync-server is the optional half: the same store a local folder
// provides, reached over HTTP so machines that do not share a folder can sync.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/linusfr/ffxiv-sync/internal/manifest"
	"github.com/linusfr/ffxiv-sync/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	token := os.Getenv("FFSYNC_TOKEN")
	if token == "" {
		log.Error("FFSYNC_TOKEN is required")
		os.Exit(1)
	}

	root := os.Getenv("FFSYNC_DATA")
	if root == "" {
		root = "/data"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Error("cannot use the data directory", "dir", root, "error", err)
		os.Exit(1)
	}

	address := os.Getenv("FFSYNC_ADDR")
	if address == "" {
		address = ":8771"
	}

	keep := 20
	if raw := os.Getenv("FFSYNC_KEEP"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			log.Error("FFSYNC_KEEP must be a positive number", "value", raw)
			os.Exit(1)
		}
		keep = parsed
	}

	server := &server{store: &store.Dir{Root: root, Keep: keep}, token: token, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /v1/current", server.guard(server.current))
	mux.HandleFunc("GET /v1/blob/{hash}", server.guard(server.blob))
	mux.HandleFunc("POST /v1/commit", server.guard(server.commit))

	log.Info("listening", "address", address, "data", root)

	listener := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Settings uploads are a few megabytes over a home connection.
		WriteTimeout: 2 * time.Minute,
		ReadTimeout:  2 * time.Minute,
	}
	if err := listener.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

type server struct {
	store *store.Dir
	token string
	log   *slog.Logger
}

// Guard checks the bearer token in constant time, so a wrong token tells an
// attacker nothing about how wrong it was.
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		given, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(s.token)) != 1 {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func (s *server) current(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Current(r.Context())
	if err != nil {
		s.fail(w, "reading the manifest", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (s *server) blob(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !plausibleHash(hash) {
		http.Error(w, "not a hash", http.StatusBadRequest)
		return
	}

	data, err := s.store.Blob(r.Context(), hash)
	if os.IsNotExist(err) {
		http.Error(w, "no such blob", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, "reading a blob", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (s *server) commit(w http.ResponseWriter, r *http.Request) {
	var body store.Commit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<20)).Decode(&body); err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if body.Manifest == nil {
		http.Error(w, "no manifest", http.StatusBadRequest)
		return
	}

	// A blob whose contents do not match its name would poison every future
	// pull, so the name is checked rather than trusted.
	for hash, data := range body.Blobs {
		if manifest.Hash(data) != hash {
			http.Error(w, "blob does not match its hash: "+hash, http.StatusBadRequest)
			return
		}
	}

	err := s.store.Commit(r.Context(), body.Manifest, body.Blobs)
	if errors.Is(err, store.ErrStale) {
		http.Error(w, "stale: pull first", http.StatusConflict)
		return
	}
	if err != nil {
		s.fail(w, "committing", err, http.StatusInternalServerError)
		return
	}

	s.log.Info("committed",
		"generation", body.Manifest.Generation,
		"device", body.Manifest.Device,
		"blobs", len(body.Blobs),
		"files", len(body.Manifest.Entries))

	w.WriteHeader(http.StatusCreated)
}

func (s *server) fail(w http.ResponseWriter, what string, err error, status int) {
	s.log.Error(what, "error", err)
	http.Error(w, fmt.Sprintf("%s failed", what), status)
}

// PlausibleHash keeps path traversal out of blob names.
func plausibleHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}

	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
