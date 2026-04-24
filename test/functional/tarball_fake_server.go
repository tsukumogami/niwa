package functional

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// tarballFakeServer is the test counterpart of the GitHub REST API
// endpoints internal/github/fetch.go consumes (HeadCommit + FetchTarball).
// It runs on httptest.Server, lets scenarios configure responses per
// (owner, repo, ref) tuple, and records each request so tests can
// assert "the second apply made no tarball request" or "the
// If-None-Match header was sent."
//
// Wire it into the niwa binary under test by setting NIWA_GITHUB_API_URL
// to the server's URL before the niwa subprocess starts.
type tarballFakeServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	tarballs map[string][]byte    // key: owner/repo/ref → tarball bytes
	commits  map[string]string    // key: owner/repo/ref → commit oid
	etags    map[string]string    // key: owner/repo/ref → ETag
	statuses map[string]int       // key: owner/repo/ref → status code override
	renames  map[string]string    // key: owner/repo → "neworg/newrepo" target
	requests []tarballFakeRequest // request log
}

type tarballFakeRequest struct {
	Method string
	Path   string
	Header http.Header
}

func newTarballFakeServer() *tarballFakeServer {
	s := &tarballFakeServer{
		tarballs: map[string][]byte{},
		commits:  map[string]string{},
		etags:    map[string]string{},
		statuses: map[string]int{},
		renames:  map[string]string{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL returns the base URL the niwa client should treat as the API
// root (i.e., what to set NIWA_GITHUB_API_URL to).
func (s *tarballFakeServer) URL() string { return s.srv.URL }

// Close shuts down the underlying httptest.Server. Safe to call
// multiple times.
func (s *tarballFakeServer) Close() { s.srv.Close() }

// SetTarball configures the gzipped tarball returned for
// /repos/{owner}/{repo}/tarball/{ref}. tarballEntries maps wrapper-
// prefixed file paths to their contents (use entries ending in "/"
// for directories). The wrapper directory should be the GitHub
// convention <owner>-<repo>-<sha>.
func (s *tarballFakeServer) SetTarball(owner, repo, ref string, entries map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tarballs[key(owner, repo, ref)] = buildGzippedTarball(entries)
}

// SetCommit configures the oid returned for the commits SHA endpoint.
func (s *tarballFakeServer) SetCommit(owner, repo, ref, oid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits[key(owner, repo, ref)] = oid
}

// SetETag configures the ETag returned for both HeadCommit and
// FetchTarball responses against the given key.
func (s *tarballFakeServer) SetETag(owner, repo, ref, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.etags[key(owner, repo, ref)] = etag
}

// SetStatus forces a status-code override for both endpoints against
// the given key. Use to simulate 401, 403, 404, etc.
func (s *tarballFakeServer) SetStatus(owner, repo, ref string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[key(owner, repo, ref)] = status
}

// SetRename simulates a 301 redirect from /repos/{oldOwner}/{oldRepo}/...
// to /repos/{newOwner}/{newRepo}/... per PRD R18 (repo rename).
func (s *tarballFakeServer) SetRename(oldOwner, oldRepo, newOwner, newRepo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renames[oldOwner+"/"+oldRepo] = newOwner + "/" + newRepo
}

// Requests returns a snapshot of the request log.
func (s *tarballFakeServer) Requests() []tarballFakeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tarballFakeRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// CountRequests returns the number of requests whose path contains
// the given substring. Useful for assertions like "the second apply
// made zero tarball requests."
func (s *tarballFakeServer) CountRequests(pathSubstring string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, r := range s.requests {
		if strings.Contains(r.Path, pathSubstring) {
			count++
		}
	}
	return count
}

// ResetLog clears the request log without affecting configured responses.
func (s *tarballFakeServer) ResetLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *tarballFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, tarballFakeRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
	})
	s.mu.Unlock()

	// Path shape: /repos/{owner}/{repo}/{op}/{ref}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(segments) < 5 || segments[0] != "repos" {
		http.NotFound(w, r)
		return
	}
	owner, repo, op, ref := segments[1], segments[2], segments[3], segments[4]

	// Rename redirect short-circuits everything else.
	s.mu.Lock()
	if newPair, ok := s.renames[owner+"/"+repo]; ok {
		newPath := strings.Replace(r.URL.Path, owner+"/"+repo, newPair, 1)
		s.mu.Unlock()
		http.Redirect(w, r, s.srv.URL+newPath, http.StatusMovedPermanently)
		return
	}
	if status, ok := s.statuses[key(owner, repo, ref)]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	if etag, ok := s.etags[key(owner, repo, ref)]; ok {
		w.Header().Set("ETag", etag)
		// Honor If-None-Match.
		if r.Header.Get("If-None-Match") == etag {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	s.mu.Unlock()

	switch op {
	case "commits":
		oid := s.commitOIDFor(owner, repo, ref)
		if oid == "" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") == "application/vnd.github.sha" {
			_, _ = w.Write([]byte(oid))
			return
		}
		// Default JSON response (not used by HeadCommit but keeps the
		// fake usable for ad-hoc tests).
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": oid})
	case "tarball":
		body := s.tarballFor(owner, repo, ref)
		if body == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(body)
	default:
		http.NotFound(w, r)
	}
}

func (s *tarballFakeServer) tarballFor(owner, repo, ref string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tarballs[key(owner, repo, ref)]
}

func (s *tarballFakeServer) commitOIDFor(owner, repo, ref string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits[key(owner, repo, ref)]
}

func key(owner, repo, ref string) string {
	return fmt.Sprintf("%s/%s@%s", owner, repo, ref)
}

func buildGzippedTarball(entries map[string]string) []byte {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	// Directories first.
	for name := range entries {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir})
	}
	// Files.
	for name, body := range entries {
		if strings.HasSuffix(name, "/") {
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return raw.Bytes()
}
