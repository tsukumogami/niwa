package mirror

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// stateSchema is the state file's format version.
const stateSchema = 1

// stateMax bounds how much of a state file is read. A real one lists a few
// packs; anything near this size wasn't written by niwa.
const stateMax = 1 << 20

// packTrailerLen is the length of a pack's trailing checksum: the SHA-1 of
// everything before it. Every store here is a SHA-1 repository.
const packTrailerLen = 20

// state is a mirror's state file, .meta/<repo-id>.json. The check trusts it
// as the record of what the store held when niwa last built it.
type state struct {
	Schema int    `json:"schema"`
	RepoID string `json:"repo_id"`
	// URL is the config's URL with userinfo, query and fragment stripped.
	// It's a record, never cloned from.
	URL string `json:"url"`
	// Store is the link target the build swapped in, ".store/<name>".
	Store string      `json:"store"`
	Packs []packEntry `json:"packs"`
}

// packEntry is one pack in a store's manifest.
type packEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	CtimeNs int64  `json:"ctime_ns"`
	Trailer string `json:"trailer"`
}

// metaDir holds the state files and the mirror log.
func metaDir(mdir string) string { return filepath.Join(mdir, ".meta") }

// statePath is the state file of repoID.
func statePath(mdir, repoID string) string {
	return filepath.Join(metaDir(mdir), repoID+".json")
}

// readState reads and decodes repoID's state file. Anything but a regular
// file is refused.
func readState(mdir, repoID string) (state, error) {
	var st state
	path := statePath(mdir, repoID)
	info, err := os.Lstat(path)
	if err != nil {
		return st, err
	}
	if !info.Mode().IsRegular() {
		return st, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, stateMax))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("decoding %s: %w", path, err)
	}
	if st.Schema != stateSchema || st.RepoID != repoID {
		return st, fmt.Errorf("%s has schema %d for %q, want schema %d for %q", path, st.Schema, st.RepoID, stateSchema, repoID)
	}
	return st, nil
}

// writeStateTemp writes st to a temporary name in .meta/ and returns it. The
// caller renames it over the state file once the store it names is linked,
// so the state file never names a store borrowers can't reach.
func writeStateTemp(mdir string, st state) (string, error) {
	data, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	suffix, err := randHex()
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(metaDir(mdir), "."+st.RepoID+".json-"+suffix)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("writing the state file: %w", err)
	}
	_, werr := f.Write(append(data, '\n'))
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("writing the state file: %w", werr)
	}
	return tmp, nil
}

// manifest lists every .pack under store's objects/pack/ with its size, ctime
// and trailing checksum, sorted by name.
func manifest(store string) ([]packEntry, error) {
	dir := filepath.Join(store, "objects", "pack")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	packs := []packEntry{}
	for _, e := range entries {
		// Only names git gives packs: the check refuses any other name
		// in a manifest, so listing one would fail every later check.
		if !validPackName(e.Name()) {
			continue
		}
		p, err := describePack(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		packs = append(packs, p)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Name < packs[j].Name })
	return packs, nil
}

// describePack reads one pack's manifest entry. It stats the path itself,
// never a link's target, and refuses anything but a regular file.
func describePack(path string) (packEntry, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return packEntry{}, fmt.Errorf("inspecting %s: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return packEntry{}, fmt.Errorf("%s is not a regular file", path)
	}
	trailer, err := packTrailer(path, st.Size)
	if err != nil {
		return packEntry{}, err
	}
	return packEntry{
		Name:    filepath.Base(path),
		Size:    st.Size,
		CtimeNs: st.Ctim.Nano(),
		Trailer: trailer,
	}, nil
}

// packTrailer returns a pack's last packTrailerLen bytes in hex.
func packTrailer(path string, size int64) (string, error) {
	if size < packTrailerLen {
		return "", fmt.Errorf("%s is shorter than a pack checksum", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, packTrailerLen)
	if _, err := f.ReadAt(buf, size-packTrailerLen); err != nil {
		return "", fmt.Errorf("reading the checksum of %s: %w", path, err)
	}
	return hex.EncodeToString(buf), nil
}

// recordURL is the form of a clone URL written to the state file: userinfo,
// query and fragment stripped, since any of them can carry a token. An
// scp-style address (user@host:path) loses its user the same way; it has no
// query or fragment. A plain path is kept as it is.
func recordURL(raw string) string {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		return u.String()
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		if colon := strings.Index(raw, ":"); colon > at {
			return raw[at+1:]
		}
	}
	return raw
}
