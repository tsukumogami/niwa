package mirror

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// Identity is remoteIdentity of URL: the remote the store was cloned
	// from, whatever spelling named it. It's written for readers of the
	// file; niwa itself derives it from URL on every read (readState), so a
	// file written before the field existed, or one whose identity
	// disagrees with its URL, is judged by its URL.
	Identity string `json:"identity"`
	// Store is the link target the build swapped in, ".store/<name>".
	Store string      `json:"store"`
	Packs []packEntry `json:"packs"`
	// LastRefresh is when the store was last brought up to date from the
	// remote: by a refresh's fetch, or by the clone a restore made. UTC,
	// RFC 3339. A state file written before refreshes existed has none.
	LastRefresh string `json:"last_refresh,omitempty"`
	// LastDeepVerify is when the store's objects were last all checked: a
	// restore's build, whose clone ran under fsckObjects and whose salvaged
	// packs each passed git verify-pack, or a refresh's daily deep verify,
	// which runs verify-pack on every pack before the fetch (the packs that
	// fetch brings arrive under fsckObjects).
	LastDeepVerify string `json:"last_deep_verify,omitempty"`
}

// stateTime formats a time for the state file's timestamps, in whole
// seconds.
func stateTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

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
// file is refused: it's opened without following a link and checked as the
// open file (openRegular), so a link swapped in after a look is never read
// through.
func readState(mdir, repoID string) (state, error) {
	var st state
	path := statePath(mdir, repoID)
	f, err := openRegular(path)
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
	// v0.43.0 wrote no identity. It's derived here, so the next write of a
	// state read from such a file carries it.
	st.Identity = remoteIdentity(st.URL)
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

// defaultPorts are the ports remoteIdentity drops, by URL scheme: a remote
// named with its scheme's default port is the same remote named without it.
var defaultPorts = map[string]string{
	"ssh":     "22",
	"git+ssh": "22",
	"ssh+git": "22",
	"git":     "9418",
	"http":    "80",
	"https":   "443",
}

// remoteIdentity is the remote a clone URL names, independent of how it's
// spelled: the host, lowercased, with its port when that isn't the scheme's
// default, then the path with any trailing slashes and one trailing ".git"
// removed. Scheme and user are not part of it, so the SSH and HTTPS
// spellings of one repo, or the same URL with another user, give the same
// identity, while another host, another port or another path doesn't.
//
// It reads the URL forms git takes (scheme://[user@]host[:port]/path), the
// scp-like form ([user@]host:path, a colon before any slash), and a local
// path. A file:// URL and a local path give the cleaned path alone, so the
// two spellings of one local repo compare as one remote. A URL that doesn't
// parse gives "", as recordURL does.
func remoteIdentity(raw string) string {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		path := identityPath(u.Path)
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return path
		}
		if port := u.Port(); port != "" && port != defaultPorts[strings.ToLower(u.Scheme)] {
			host = net.JoinHostPort(host, port)
		}
		return host + "/" + strings.TrimLeft(path, "/")
	}
	if colon := strings.Index(raw, ":"); colon > 0 && !strings.Contains(raw[:colon], "/") {
		host := raw[:colon]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return strings.ToLower(host) + "/" + strings.TrimLeft(identityPath(raw[colon+1:]), "/")
	}
	return identityPath(raw)
}

// identityPath is a remote's path with its trailing slashes and one
// trailing ".git" removed, so "o/r", "o/r.git" and "o/r.git/" are one path.
func identityPath(p string) string {
	p = strings.TrimRight(p, "/")
	return strings.TrimRight(strings.TrimSuffix(p, ".git"), "/")
}
