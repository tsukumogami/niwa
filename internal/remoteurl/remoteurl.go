// Package remoteurl parses and compares the git URLs the removal rule decides
// with: the clone URLs the workspace configuration gives, the proof_remotes
// prefixes it lists, and the remote URLs read from a repository.
//
// The rule never compares a URL it would have to clean first. A URL whose path
// holds a dot or empty segment, that carries percent-encoding, a backslash, a
// control character or userinfo is refused rather than normalized, because the
// string niwa compares isn't the request git sends: libcurl removes dot
// segments before it sends and a server may decode percent-escapes, so
// https://host/dana/../mallory/repo would start with the prefix
// https://host/dana/ and still reach host/mallory/repo.
//
// It is a leaf package so the configuration loader can refuse a bad prefix at
// load time with the same parser the rule uses.
package remoteurl

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// URL is a parsed, validated git URL.
type URL struct {
	// Scheme is "https", "ssh" or, in the test build only, "file". The
	// scp-like form host:path is parsed as ssh.
	Scheme string
	// User is "git" or empty; only ssh carries one.
	User string
	// Host is lowercased; empty for a file URL.
	Host string
	// Port is explicit, the scheme's default when the URL names none; empty
	// for a file URL.
	Port string
	// Path starts with "/" and has no trailing "/". It keeps a trailing
	// ".git", which Key drops.
	Path string
}

// defaultPorts are the ports a URL that names none is compared on.
var defaultPorts = map[string]string{"https": "443", "ssh": "22"}

// Parse parses raw and refuses it when the rule can't compare it safely. The
// error text is the reason, written so it never repeats the userinfo it
// found. allowFile admits a file:// URL, which only the test build ever does.
func Parse(raw string, allowFile bool) (URL, error) {
	if raw == "" {
		return URL{}, errors.New("the URL is empty")
	}
	// A <transport>::<address> URL runs a remote helper (ext:: runs any
	// command); it is named before anything else in it is judged.
	if i := strings.Index(raw, "::"); i > 0 && isTransportName(raw[:i]) {
		return URL{}, fmt.Errorf("scheme not allowed: %s", schemeName(strings.ToLower(raw[:i])))
	}
	if err := checkChars(raw); err != nil {
		return URL{}, err
	}
	if strings.HasPrefix(raw, "-") {
		return URL{}, errors.New("the URL starts with -")
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || strings.Contains(scheme, "/") {
		return parseSCP(raw)
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "https", "ssh":
	case "file":
		if !allowFile {
			return URL{}, errors.New("scheme not allowed: file")
		}
		return parseFile(rest)
	default:
		return URL{}, fmt.Errorf("scheme not allowed: %s", schemeName(scheme))
	}
	authority, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	u := URL{Scheme: scheme}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		user := authority[:at]
		authority = authority[at+1:]
		if scheme == "https" {
			return URL{}, errors.New("the URL carries userinfo")
		}
		if user != "git" {
			return URL{}, errors.New("the URL carries userinfo other than the user git")
		}
		u.User = user
	}
	host, port, err := splitHostPort(authority)
	if err != nil {
		return URL{}, err
	}
	if port == "" {
		port = defaultPorts[scheme]
	}
	u.Host, u.Port = host, port
	if u.Path, err = cleanPath(path); err != nil {
		return URL{}, err
	}
	return u, nil
}

// ParsePrefix parses a [session.removal] proof_remotes entry: an https:// or
// ssh:// URL ending in "/" with a host and at least one path segment.
func ParsePrefix(raw string) (URL, error) {
	if !strings.HasSuffix(raw, "/") {
		return URL{}, errors.New("a proof remote prefix must end in /")
	}
	if !strings.Contains(raw, "://") {
		return URL{}, errors.New("a proof remote prefix must be an https:// or ssh:// URL")
	}
	u, err := Parse(raw, false)
	if err != nil {
		return URL{}, err
	}
	if u.Scheme != "https" && u.Scheme != "ssh" {
		return URL{}, errors.New("a proof remote prefix must be an https:// or ssh:// URL")
	}
	if u.Path == "/" || u.Path == "" {
		return URL{}, errors.New("a proof remote prefix needs at least one path segment")
	}
	return u, nil
}

// Key is the comparison form: scheme and host lowercased, the port explicit,
// a trailing "/" or ".git" dropped, the path otherwise as written.
func (u URL) Key() string {
	return u.authority(true) + strings.TrimSuffix(u.Path, ".git")
}

// String is the URL to hand git: the authority with a default port left out,
// and the path as parsed.
func (u URL) String() string {
	return u.authority(false) + u.Path
}

func (u URL) authority(explicitPort bool) string {
	if u.Scheme == "file" {
		return "file://"
	}
	var b strings.Builder
	b.WriteString(u.Scheme + "://")
	if u.User != "" {
		b.WriteString(u.User + "@")
	}
	b.WriteString(u.Host)
	if explicitPort || u.Port != defaultPorts[u.Scheme] {
		b.WriteString(":" + u.Port)
	}
	return b.String()
}

// SameAuthority reports whether u and p name the same scheme, user, host and
// port.
func (u URL) SameAuthority(p URL) bool {
	return u.Scheme == p.Scheme && u.User == p.User && u.Host == p.Host && u.Port == p.Port
}

// UnderPrefix reports whether u sits under the prefix p: the same scheme,
// user, host and port, and p's path segments followed by at least one more of
// u's. It matches whole segments only, so https://host/dana/ never matches
// https://host/dana-evil/repo, and never matches a file URL.
func (u URL) UnderPrefix(p URL) bool {
	if u.Scheme == "file" || p.Scheme == "file" || !u.SameAuthority(p) {
		return false
	}
	prefix := strings.TrimSuffix(p.Path, "/") + "/"
	return strings.HasPrefix(u.Path, prefix) && len(u.Path) > len(prefix)
}

// Rebuild is the URL niwa contacts for a remote that matched the prefix p:
// scheme, user, host and port from p, and only the path from u.
func (u URL) Rebuild(p URL) string {
	return URL{Scheme: p.Scheme, User: p.User, Host: p.Host, Port: p.Port, Path: u.Path}.String()
}

// LastSegment is the last segment of the path, with a trailing ".git"
// dropped: the name a repository cloned from u would get.
func (u URL) LastSegment() string {
	p := strings.TrimSuffix(u.Path, ".git")
	return p[strings.LastIndex(p, "/")+1:]
}

// checkChars refuses the characters a URL the rule compares must not hold.
func checkChars(raw string) error {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c < 0x20 || c == 0x7f:
			return errors.New("the URL carries a control character")
		case c == ' ':
			return errors.New("the URL carries a space")
		case c == '\\':
			return errors.New("the URL carries a backslash")
		case c == '%':
			return errors.New("the URL carries percent-encoding")
		case c >= 0x80:
			return errors.New("the URL carries a non-ASCII character")
		}
	}
	return nil
}

// isTransportName reports whether s can name a git remote helper: letters,
// digits, "+", "-" and ".", starting with a letter.
func isTransportName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return false
		}
	}
	return s != ""
}

// schemeName is a scheme as an error names it, cut so a long or odd one
// can't fill the evidence.
func schemeName(s string) string {
	if len(s) > 16 {
		return s[:16] + "..."
	}
	return s
}

// parseSCP parses the scp-like form [git@]host:path, which git reads as ssh.
// Anything else without "://" is a local path or a transport such as ext::,
// both refused.
func parseSCP(raw string) (URL, error) {
	colon := strings.Index(raw, ":")
	slash := strings.Index(raw, "/")
	if colon < 0 || (slash >= 0 && slash < colon) {
		return URL{}, errors.New("scheme not allowed: a local path")
	}
	authority, path := raw[:colon], raw[colon+1:]
	u := URL{Scheme: "ssh", Port: defaultPorts["ssh"]}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		if authority[:at] != "git" {
			return URL{}, errors.New("the URL carries userinfo other than the user git")
		}
		u.User = "git"
		authority = authority[at+1:]
	}
	if strings.Contains(authority, "[") || strings.Contains(authority, "]") {
		return URL{}, errors.New("the host does not parse")
	}
	host, err := checkHost(authority)
	if err != nil {
		return URL{}, err
	}
	u.Host = host
	if strings.HasPrefix(path, "-") {
		return URL{}, errors.New("the path starts with -")
	}
	if u.Path, err = cleanPath("/" + strings.TrimPrefix(path, "/")); err != nil {
		return URL{}, err
	}
	return u, nil
}

// parseFile parses what follows file://: an empty host and an absolute path.
func parseFile(rest string) (URL, error) {
	if !strings.HasPrefix(rest, "/") {
		return URL{}, errors.New("a file URL must name no host")
	}
	p, err := cleanPath(rest)
	if err != nil {
		return URL{}, err
	}
	return URL{Scheme: "file", Path: p}, nil
}

// splitHostPort splits an authority with no userinfo into its lowercased
// host and its port, empty when it names none.
func splitHostPort(authority string) (string, string, error) {
	host, port := authority, ""
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end < 0 {
			return "", "", errors.New("the host does not parse")
		}
		host = authority[:end+1]
		switch rest := authority[end+1:]; {
		case rest == "":
		case strings.HasPrefix(rest, ":"):
			port = rest[1:]
		default:
			return "", "", errors.New("the host does not parse")
		}
		if strings.Trim(host[1:end], "0123456789abcdefABCDEF:.") != "" || end == 1 {
			return "", "", errors.New("the host does not parse")
		}
		host = strings.ToLower(host)
	} else {
		if i := strings.LastIndex(authority, ":"); i >= 0 {
			host, port = authority[:i], authority[i+1:]
		}
		var err error
		if host, err = checkHost(host); err != nil {
			return "", "", err
		}
	}
	if port != "" || strings.HasSuffix(authority, ":") {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strings.Trim(port, "0123456789") != "" {
			return "", "", errors.New("the port does not parse")
		}
		port = strconv.Itoa(n)
	}
	return host, port, nil
}

// checkHost lowercases a host name and refuses an empty one, one starting
// with "-", and one holding anything but letters, digits, dots, dashes and
// underscores.
func checkHost(host string) (string, error) {
	switch {
	case host == "":
		return "", errors.New("the URL names no host")
	case strings.HasPrefix(host, "-"):
		return "", errors.New("the host starts with -")
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return "", errors.New("the host does not parse")
		}
	}
	return strings.ToLower(host), nil
}

// cleanPath checks an absolute path without changing it: one trailing "/"
// is dropped, and a ".", ".." or empty segment, or a first segment starting
// with "-", is refused.
func cleanPath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", errors.New("the path is not absolute")
	}
	trimmed := strings.TrimSuffix(p, "/")
	if trimmed == "" {
		return "/", nil
	}
	for i, seg := range strings.Split(trimmed[1:], "/") {
		switch {
		case seg == "":
			return "", errors.New("the path holds an empty segment")
		case seg == "." || seg == "..":
			return "", errors.New("the path holds a dot segment")
		case i == 0 && strings.HasPrefix(seg, "-"):
			return "", errors.New("the path starts with -")
		}
	}
	return trimmed, nil
}
