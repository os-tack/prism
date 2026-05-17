package registry

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sampleIndex is a small valid registry index used by most tests.
const sampleIndex = `{
  "version": 1,
  "packages": {
    "billing-skills": {
      "source": "github.com/os-tack/prism-pkg-billing-skills",
      "default_ref": "v1.0.0",
      "description": "Billing-domain skills bundle"
    },
    "pdf-editing": {
      "source": "github.com/anthropic/skills/pdf-editing"
    }
  }
}`

// indexServer returns an httptest server that serves the given body at the
// root path with status 200, and a counter for hit-count assertions.
func indexServer(t *testing.T, body string) (url string, hits *int) {
	t.Helper()
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &count
}

// failingServer returns a URL that always 500s.
func failingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// withCacheDir gives each test an isolated cache directory.
func withCacheDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// --- isBareName ---------------------------------------------------------

func TestIsBareName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"billing-skills", true},
		{"pdf", true},
		{"a", true},
		{"a1b2", true},
		{"0test", true},
		{"", false},
		{"github.com/foo/bar", false},
		{"./local", false},
		{"/abs/path", false},
		{"name@v1.0", false},
		{"Foo", false}, // uppercase rejected
		{"with_underscore", false},
		{"-leading-dash", false},
		{strings.Repeat("a", 65), false}, // too long
	}
	for _, c := range cases {
		if got := isBareName(c.in); got != c.want {
			t.Errorf("isBareName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --- resolveURL precedence ---------------------------------------------

func TestResolveURL_FlagBeatsEnv(t *testing.T) {
	t.Setenv(RegistryEnvVar, "https://env.example/index.json")
	got := resolveURL("https://flag.example/index.json")
	if got != "https://flag.example/index.json" {
		t.Fatalf("flag should win, got %q", got)
	}
}

func TestResolveURL_EnvBeatsDefault(t *testing.T) {
	t.Setenv(RegistryEnvVar, "https://env.example/index.json")
	got := resolveURL("")
	if got != "https://env.example/index.json" {
		t.Fatalf("env should win, got %q", got)
	}
}

func TestResolveURL_Default(t *testing.T) {
	t.Setenv(RegistryEnvVar, "")
	got := resolveURL("")
	if got != DefaultRegistryURL {
		t.Fatalf("default should win, got %q", got)
	}
}

// --- fetch + cache flows -----------------------------------------------

func TestResolveBareName_FetchAndCache(t *testing.T) {
	url, hits := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)

	opts := RegistryOptions{URL: url, CacheDir: cacheDir}

	src, ref, err := resolveBareName("billing-skills", opts)
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if src != "github.com/os-tack/prism-pkg-billing-skills" {
		t.Errorf("source = %q", src)
	}
	if ref != "v1.0.0" {
		t.Errorf("ref = %q", ref)
	}
	if *hits != 1 {
		t.Errorf("expected 1 hit, got %d", *hits)
	}

	// Second lookup should be served from cache (no new hit).
	if _, _, err := resolveBareName("pdf-editing", opts); err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if *hits != 1 {
		t.Errorf("cache should be reused; got %d hits", *hits)
	}

	// Cache file should exist.
	cachePath := filepath.Join(cacheDir, "registry-index.json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
}

func TestResolveBareName_NotFound(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: url, CacheDir: cacheDir}
	_, _, err := resolveBareName("does-not-exist", opts)
	if err == nil {
		t.Fatal("expected error for missing package")
	}
	if !strings.Contains(err.Error(), `"does-not-exist"`) {
		t.Errorf("error should quote name: %v", err)
	}
	if !strings.Contains(err.Error(), "not found in central registry") {
		t.Errorf("error should mention central registry: %v", err)
	}
}

func TestResolveBareName_RefreshForcesFetch(t *testing.T) {
	url, hits := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: url, CacheDir: cacheDir}

	if _, _, err := resolveBareName("billing-skills", opts); err != nil {
		t.Fatalf("first: %v", err)
	}
	if *hits != 1 {
		t.Fatalf("hits=%d", *hits)
	}

	opts.Refresh = true
	if _, _, err := resolveBareName("billing-skills", opts); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if *hits != 2 {
		t.Errorf("refresh should re-fetch, got %d hits", *hits)
	}
}

func TestResolveBareName_FetchFailFallsBackToCache(t *testing.T) {
	// Prime cache with a working server.
	goodURL, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	primeOpts := RegistryOptions{URL: goodURL, CacheDir: cacheDir}
	if _, _, err := resolveBareName("billing-skills", primeOpts); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Now point at a failing URL, force refresh so we hit the network.
	// Fetch fails -> stale cache fallback kicks in.
	var warnings []string
	failURL := failingServer(t)
	opts := RegistryOptions{
		URL:      failURL,
		CacheDir: cacheDir,
		Refresh:  true,
		Warnf:    func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) },
	}
	src, _, err := resolveBareName("billing-skills", opts)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if src != "github.com/os-tack/prism-pkg-billing-skills" {
		t.Errorf("source = %q", src)
	}
	if len(warnings) == 0 {
		t.Error("expected warning about cache fallback")
	}
}

func TestResolveBareName_FetchFailNoCache(t *testing.T) {
	cacheDir := withCacheDir(t)
	failURL := failingServer(t)
	opts := RegistryOptions{URL: failURL, CacheDir: cacheDir}
	_, _, err := resolveBareName("billing-skills", opts)
	if err == nil {
		t.Fatal("expected error when fetch fails and no cache exists")
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Errorf("error should mention fetch: %v", err)
	}
}

func TestResolveBareName_NoFetchUsesCache(t *testing.T) {
	url, hits := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)

	// Prime cache.
	if _, _, err := resolveBareName("billing-skills", RegistryOptions{URL: url, CacheDir: cacheDir}); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if *hits != 1 {
		t.Fatalf("hits=%d", *hits)
	}

	// NoFetch should use cache without hitting the server.
	opts := RegistryOptions{URL: url, CacheDir: cacheDir, NoFetch: true}
	src, _, err := resolveBareName("billing-skills", opts)
	if err != nil {
		t.Fatalf("no-fetch: %v", err)
	}
	if src != "github.com/os-tack/prism-pkg-billing-skills" {
		t.Errorf("source = %q", src)
	}
	if *hits != 1 {
		t.Errorf("no-fetch should not hit network; got %d", *hits)
	}
}

func TestResolveBareName_NoFetchWithoutCache(t *testing.T) {
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: "https://unused.example", CacheDir: cacheDir, NoFetch: true}
	_, _, err := resolveBareName("billing-skills", opts)
	if err == nil {
		t.Fatal("expected error when --no-fetch but no cache")
	}
	if !strings.Contains(err.Error(), "no-fetch") && !strings.Contains(err.Error(), "cache") {
		t.Errorf("error should mention no-fetch/cache: %v", err)
	}
}

func TestResolveBareName_StaleCacheRefreshesViaTTL(t *testing.T) {
	url, hits := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)

	// Prime cache.
	if _, _, err := resolveBareName("billing-skills", RegistryOptions{URL: url, CacheDir: cacheDir}); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Age the cache file past TTL by rewriting its mtime.
	cachePath := filepath.Join(cacheDir, "registry-index.json")
	old := time.Now().Add(-(cacheTTL + time.Hour))
	if err := os.Chtimes(cachePath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Lookup should re-fetch.
	if _, _, err := resolveBareName("billing-skills", RegistryOptions{URL: url, CacheDir: cacheDir}); err != nil {
		t.Fatalf("after-ttl: %v", err)
	}
	if *hits != 2 {
		t.Errorf("expected refetch after TTL expiry, got %d hits", *hits)
	}
}

// --- env-var override end-to-end ---------------------------------------

func TestResolveBareName_EnvVarOverride(t *testing.T) {
	url, hits := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	t.Setenv(RegistryEnvVar, url)

	// No URL on opts -> should pick up env.
	opts := RegistryOptions{CacheDir: cacheDir}
	src, _, err := resolveBareName("billing-skills", opts)
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if src != "github.com/os-tack/prism-pkg-billing-skills" {
		t.Errorf("source = %q", src)
	}
	if *hits != 1 {
		t.Errorf("hits = %d", *hits)
	}
}

// --- parse failures -----------------------------------------------------

func TestParseIndex_UnsupportedVersion(t *testing.T) {
	_, err := parseIndex([]byte(`{"version": 2, "packages": {}}`))
	if err == nil {
		t.Fatal("expected version error")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention version: %v", err)
	}
}

func TestParseIndex_MissingPackages(t *testing.T) {
	idx, err := parseIndex([]byte(`{"version": 1}`))
	if err != nil {
		t.Fatalf("packages should default to empty map: %v", err)
	}
	if idx.Packages == nil {
		t.Error("packages map should be initialized")
	}
}

func TestParseIndex_Malformed(t *testing.T) {
	_, err := parseIndex([]byte(`not json`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// --- resolveSource end-to-end ------------------------------------------

func TestResolveSource_BareName_UsesIndexRef(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: url, CacheDir: cacheDir}

	src, ref, err := resolveSource("billing-skills", "", opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if src != "github.com/os-tack/prism-pkg-billing-skills" {
		t.Errorf("source = %q", src)
	}
	if ref != "v1.0.0" {
		t.Errorf("ref = %q (expected default_ref to flow through)", ref)
	}
}

func TestResolveSource_BareName_UserRefBeatsIndex(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: url, CacheDir: cacheDir}

	_, ref, err := resolveSource("billing-skills", "v2.0.0", opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ref != "v2.0.0" {
		t.Errorf("user --ref should win, got %q", ref)
	}
}

func TestResolveSource_NonBareNamePassesThrough(t *testing.T) {
	// No options needed — this should not hit the network at all.
	src, ref, err := resolveSource("github.com/foo/bar", "main", RegistryOptions{})
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if src != "github.com/foo/bar" || ref != "main" {
		t.Errorf("passthrough mangled inputs: src=%q ref=%q", src, ref)
	}

	src, ref, err = resolveSource("./local", "", RegistryOptions{})
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	if src != "./local" || ref != "" {
		t.Errorf("local passthrough wrong: src=%q ref=%q", src, ref)
	}
}

func TestResolveSource_BareName_NotFound_ReturnsError(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	opts := RegistryOptions{URL: url, CacheDir: cacheDir}

	_, _, err := resolveSource("nope", "", opts)
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// --- Install end-to-end via bare name (mock index + local stand-in)----
//
// We can't actually clone github.com/... in a unit test. Instead, prime the
// index with a *local* source so the full Install flow exercises the
// bare-name resolution branch while keeping the test hermetic.
func TestInstall_BareName_LocalFallback(t *testing.T) {
	// Build a local "package" we can install from.
	pkgDir := makePackage(t, "name: my-pkg\nschema: 1\ncontents:\n  - skills/pdf-editing\n")

	// Index points at the absolute path of that package (resolveSource
	// only intercepts the bare-name branch; non-bare strings pass through
	// to materializeSource which already handles absolute paths).
	indexBody := fmt.Sprintf(`{
  "version": 1,
  "packages": {
    "my-pkg": {
      "source": %q
    }
  }
}`, pkgDir)
	url, _ := indexServer(t, indexBody)
	cacheDir := withCacheDir(t)

	proj := makeProject(t)
	pkg, err := Install(proj, "my-pkg", InstallOptions{
		Registry: RegistryOptions{URL: url, CacheDir: cacheDir},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if pkg.Name != "my-pkg" {
		t.Errorf("name = %q", pkg.Name)
	}
	// Source should be the resolved absolute path, not the bare name.
	if pkg.Source != pkgDir {
		t.Errorf("source should be resolved, got %q (want %q)", pkg.Source, pkgDir)
	}
}

func TestInstall_BareName_NotFound_ReturnsError(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	proj := makeProject(t)
	_, err := Install(proj, "nonexistent", InstallOptions{
		Registry: RegistryOptions{URL: url, CacheDir: cacheDir},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found in central registry") {
		t.Errorf("error should be a clear central-registry miss: %v", err)
	}
}

// --- sanity: a sentinel-error API parity check (not strictly needed but
// guards against accidental changes to the error wrapping).
func TestResolveBareName_NotFoundIsRegistryError(t *testing.T) {
	url, _ := indexServer(t, sampleIndex)
	cacheDir := withCacheDir(t)
	_, _, err := resolveBareName("zzz", RegistryOptions{URL: url, CacheDir: cacheDir})
	if err == nil {
		t.Fatal("want err")
	}
	// Make sure the URL is in the error so users know where to look.
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error should include registry URL: %v", err)
	}
	// And not a sentinel — these are user-facing diagnostics, not
	// errors.Is-targets (we don't currently expose a sentinel for this).
	if errors.Is(err, ErrNoAgentsDir) {
		t.Error("should not be ErrNoAgentsDir")
	}
}
