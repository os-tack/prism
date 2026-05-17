package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultRegistryURL is the canonical central-registry index URL used when
// neither the --registry flag nor PRISM_REGISTRY env var is set. The repo
// itself is a future deliverable; the resolution layer just points at it.
const DefaultRegistryURL = "https://raw.githubusercontent.com/os-tack/prism-registry/main/index.json"

// RegistryEnvVar is the environment variable that overrides the default
// registry URL. Lower priority than the explicit --registry flag.
const RegistryEnvVar = "PRISM_REGISTRY"

// cacheTTL is the maximum age of the locally-cached index file before we
// refetch. Callers can bypass with RegistryOptions.Refresh.
const cacheTTL = 24 * time.Hour

// fetchTimeout caps how long we wait on the network before falling back to
// the cache (or erroring).
const fetchTimeout = 10 * time.Second

// bareNameRE matches the syntactic shape of a registry-index name:
// lowercase alphanumerics and dashes, must start with [a-z0-9], 1-64 chars,
// no dots or slashes. This is intentionally strict so we don't accidentally
// route a typo'd git URL ("github.com" missing a slash) through the index.
var bareNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// isBareName reports whether source looks like a central-registry package
// name rather than a git URL or local path.
func isBareName(source string) bool {
	if source == "" {
		return false
	}
	if strings.ContainsAny(source, "/.@") {
		return false
	}
	return bareNameRE.MatchString(source)
}

// Index is the on-disk JSON document published by the central registry.
type Index struct {
	Version  int                      `json:"version"`
	Packages map[string]*IndexPackage `json:"packages"`
}

// IndexPackage is a single entry in the central-registry index.
type IndexPackage struct {
	Source      string `json:"source"`
	DefaultRef  string `json:"default_ref,omitempty"`
	Description string `json:"description,omitempty"`
}

// RegistryOptions controls index resolution. All fields are optional; zero
// values use the defaults documented on each field.
type RegistryOptions struct {
	// URL overrides the default registry URL. Empty means: use the
	// PRISM_REGISTRY env var, else DefaultRegistryURL.
	URL string
	// Refresh forces a fresh fetch even if the cache is still valid.
	Refresh bool
	// NoFetch disables network access entirely; only the cache is consulted.
	// If no cache exists, resolution fails.
	NoFetch bool
	// CacheDir overrides the cache directory (used by tests). Empty means
	// os.UserCacheDir()/prism.
	CacheDir string
	// HTTPClient overrides the HTTP client (used by tests). Nil means a
	// default client with fetchTimeout.
	HTTPClient *http.Client
	// Warnf is called with non-fatal messages (e.g. "fetch failed, using
	// cached index"). Nil discards.
	Warnf func(format string, args ...any)
}

// resolveBareName looks up `name` in the central registry and returns the
// git source string (e.g. "github.com/os-tack/prism-pkg-billing-skills")
// plus the package's default ref (which may be empty). Returns an error if
// the index can't be loaded or the name isn't present.
func resolveBareName(name string, opts RegistryOptions) (source, defaultRef string, err error) {
	idx, src, err := loadIndex(opts)
	if err != nil {
		return "", "", err
	}
	pkg, ok := idx.Packages[name]
	if !ok {
		return "", "", fmt.Errorf("registry: %q not found in central registry %s", name, src)
	}
	if pkg.Source == "" {
		return "", "", fmt.Errorf("registry: %q in central registry %s has empty source field", name, src)
	}
	return pkg.Source, pkg.DefaultRef, nil
}

// loadIndex returns the parsed index plus the URL it was loaded from (for
// error messages). It applies the resolution policy: fresh fetch (unless
// NoFetch), fall back to cache on failure, then surface a clean error.
func loadIndex(opts RegistryOptions) (*Index, string, error) {
	url := resolveURL(opts.URL)
	cachePath, err := indexCachePath(opts.CacheDir)
	if err != nil {
		return nil, url, err
	}

	if opts.NoFetch {
		idx, err := readCachedIndex(cachePath)
		if err != nil {
			return nil, url, fmt.Errorf("registry: --no-fetch set but cache unavailable at %s: %w", cachePath, err)
		}
		return idx, url, nil
	}

	// Fast path: if the cache is fresh and we weren't asked to refresh,
	// just use it. This avoids hitting the network on every `agents add`.
	if !opts.Refresh {
		if idx, ok := freshCachedIndex(cachePath); ok {
			return idx, url, nil
		}
	}

	// Fetch over the network; on failure, fall back to a stale cache if
	// one exists, otherwise surface the fetch error.
	idx, raw, err := fetchIndex(url, opts.HTTPClient)
	if err != nil {
		if cached, cacheErr := readCachedIndex(cachePath); cacheErr == nil {
			warnf(opts, "registry: fetch %s failed (%v); using cached index at %s", url, err, cachePath)
			return cached, url, nil
		}
		return nil, url, fmt.Errorf("registry: fetch %s: %w", url, err)
	}
	// Persist the freshly-fetched bytes. Cache write failures are
	// non-fatal — we still have a parsed index in memory.
	if writeErr := writeCachedIndex(cachePath, raw); writeErr != nil {
		warnf(opts, "registry: cache write %s failed: %v", cachePath, writeErr)
	}
	return idx, url, nil
}

// resolveURL applies the precedence chain: explicit flag > env var >
// default. An empty result is never returned (DefaultRegistryURL is the
// floor).
func resolveURL(flagURL string) string {
	if flagURL != "" {
		return flagURL
	}
	if env := strings.TrimSpace(os.Getenv(RegistryEnvVar)); env != "" {
		return env
	}
	return DefaultRegistryURL
}

// indexCachePath returns the on-disk path the index is cached at. The
// directory is created on demand.
func indexCachePath(override string) (string, error) {
	dir := override
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("registry: locate user cache dir: %w", err)
		}
		dir = filepath.Join(base, "prism")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("registry: mkdir cache dir %s: %w", dir, err)
	}
	return filepath.Join(dir, "registry-index.json"), nil
}

// freshCachedIndex returns the cached index if it exists and is younger
// than cacheTTL; otherwise (false, nil). Read errors are treated as a
// cache miss (the caller will refetch).
func freshCachedIndex(path string) (*Index, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) > cacheTTL {
		return nil, false
	}
	idx, err := readCachedIndex(path)
	if err != nil {
		return nil, false
	}
	return idx, true
}

// readCachedIndex reads and parses the cache file. Returns an error if
// the file is missing or malformed.
func readCachedIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	idx, err := parseIndex(data)
	if err != nil {
		return nil, fmt.Errorf("parse cached index %s: %w", path, err)
	}
	return idx, nil
}

// writeCachedIndex writes data atomically via a sibling .tmp file + rename
// so a partial write can't corrupt the cache.
func writeCachedIndex(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fetchIndex performs the HTTP GET, validates the status code, and parses
// the body. Returns the parsed index AND the raw bytes (for caching).
func fetchIndex(url string, client *http.Client) (*Index, []byte, error) {
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	idx, err := parseIndex(body)
	if err != nil {
		return nil, nil, err
	}
	return idx, body, nil
}

// parseIndex decodes the JSON document and applies minimal validation
// (version, non-nil packages map).
func parseIndex(data []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse index json: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("unsupported index version %d (expected 1)", idx.Version)
	}
	if idx.Packages == nil {
		idx.Packages = map[string]*IndexPackage{}
	}
	return &idx, nil
}

func warnf(opts RegistryOptions, format string, args ...any) {
	if opts.Warnf != nil {
		opts.Warnf(format, args...)
	}
}

// resolveSource applies bare-name resolution: if `source` is a syntactic
// bare name, look it up in the central registry and return the rewritten
// git source plus an effective ref override. For all other shapes (local
// paths, github.com/... URLs) the inputs pass through unchanged.
//
// Ref precedence when source is a bare name:
//
//	user --ref flag > index default_ref > (none)
//
// The bare name itself never carries an @ref suffix (the regex rejects @),
// so we don't need to worry about a source-suffix beating the index ref.
func resolveSource(source, refOverride string, opts RegistryOptions) (string, string, error) {
	if !isBareName(source) {
		return source, refOverride, nil
	}
	gitSource, defaultRef, err := resolveBareName(source, opts)
	if err != nil {
		return "", "", err
	}
	if refOverride == "" {
		refOverride = defaultRef
	}
	return gitSource, refOverride, nil
}
