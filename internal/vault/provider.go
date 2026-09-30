// Package vault defines the provider interface, registry, and helpers
// that niwa uses to resolve vault:// URIs into secret.Value instances.
//
// The package implements Decision 3 of the vault-integration design:
//
//   - Provider is the four-method skeleton every backend must satisfy
//     (Name, Kind, Resolve, Close).
//   - BatchResolver is an optional extension detected by runtime type
//     assertion; backends that can resolve many refs in one RPC (e.g.,
//     Infisical's export endpoint) implement it for better latency.
//   - Factory constructs a Provider from a typed ProviderConfig.
//     Registry indexes Factories by Kind; DefaultRegistry is the
//     production-time registry that backends register into via init().
//   - Ref is the parsed form of a vault:// URI. VersionToken carries
//     the provider-opaque version identifier plus a user-facing
//     provenance string (audit-log URL, git SHA, etc.).
//
// Backends live in sub-packages (internal/vault/infisical,
// internal/vault/fake) and register themselves with DefaultRegistry
// via init(). The fake backend deliberately does NOT auto-register
// — tests that need it construct a fresh Registry via NewRegistry and
// Register the fake factory explicitly.
//
// # Serving last-resolved values when a vault can't be asked
//
// Five packages share that job; each owns one step:
//
//   - vault (errors.go, runstate.go): the vocabulary. FailureClass says
//     whether a failure is unauthenticated, unreachable or answered,
//     ClassifiedError carries it on the backend's error, and RunState
//     remembers a domain's verdict for the rest of one run. Identity
//     names a store of values.
//   - vault/infisical (bounds.go, classify.go): the backend's side. It
//     puts deadlines and output caps on every CLI call and classifies a
//     failed export from its server response and a session probe.
//   - vault/store: the files on disk, one per Identity, holding the
//     last value each key resolved to.
//   - vault/storefallback: the decorator on a provisioning run's
//     providers. It records every resolution, evicts deleted keys, and
//     serves stored values for unauthenticated or unreachable failures
//     only, reading the FailureClass rather than any sentinel.
//   - fallbacknotice: the stdlib-only collector and renderer for what
//     the fallback served, missed or couldn't write, which each command
//     surface prints.
package vault

import (
	"context"
	"net/url"
	"strings"

	"github.com/tsukumogami/niwa/internal/secret"
)

// Provider is the interface every vault backend implements.
//
// Provider methods run in the context of a single niwa apply
// invocation. Implementations that cache authenticated sessions
// should tie the cache lifetime to Close.
type Provider interface {
	// Name returns the user-facing provider name. For the anonymous
	// singular [vault.provider] shape, Name returns "". For named
	// [vault.providers.<name>] shapes, Name returns "<name>".
	Name() string

	// Kind returns the backend kind registered with the Factory
	// (e.g., "infisical", "sops", "fake").
	Kind() string

	// Resolve fetches the secret identified by ref. On success, it
	// returns the plaintext wrapped in a secret.Value together with
	// a VersionToken identifying the specific revision fetched.
	//
	// Resolve must return ErrKeyNotFound when the key does not exist
	// and ErrProviderUnreachable when the backend cannot be contacted
	// (auth failure, network error), narrowed to ErrClientNotInstalled
	// when its client binary is missing. Other errors may be wrapped
	// with secret.Errorf.
	//
	// A backend that can tell why a call failed returns the failure
	// as a *ClassifiedError built with Classify, so callers can act on
	// the class (errors.As with *FailureClass) while the text stays
	// the backend's own. A missing client carries no class.
	Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)

	// Close releases any resources held by the provider (subprocess
	// sessions, credential caches, file handles). Close is idempotent:
	// calling it twice MUST NOT return an error on the second call.
	Close() error
}

// BatchResolver is an optional Provider extension for backends that
// can resolve many refs in a single RPC. The resolver stage tests for
// this interface via runtime type assertion and prefers it when
// available.
//
// Implementations MUST return a BatchResult for every input ref in
// the same order; missing keys are signaled by setting BatchResult.Err
// to ErrKeyNotFound rather than by dropping the result.
type BatchResolver interface {
	ResolveBatch(ctx context.Context, refs []Ref) ([]BatchResult, error)
}

// Factory constructs Provider instances from a typed ProviderConfig.
// Backends register Factory instances with a Registry (typically
// DefaultRegistry) via init().
type Factory interface {
	// Kind returns the backend kind this factory produces. Must be
	// unique within a Registry.
	Kind() string

	// Open constructs a Provider from the given config. The caller
	// (the resolver stage) is responsible for calling Close on the
	// returned Provider, typically via Bundle.CloseAll.
	Open(ctx context.Context, config ProviderConfig) (Provider, error)
}

// Ref is the parsed form of a vault:// URI. See ParseRef.
type Ref struct {
	// ProviderName names which provider to resolve against. Empty
	// string selects the anonymous singular provider declared via
	// [vault.provider]; a non-empty value selects a named provider
	// declared via [vault.providers.<name>].
	ProviderName string

	// Path carries the folder-path segments of an anonymous-provider
	// URI (everything between "vault://" and the final "/"). Non-empty
	// only for URIs of the form vault://<segments.../>/<key> in files
	// that use an anonymous [vault.provider]. Named-provider URIs leave
	// this empty. Backends that understand folder paths (e.g. Infisical)
	// honor Path on each resolve, overriding any Factory.Open-time
	// default path when non-empty. Backends without folder semantics
	// (e.g. sops) ignore Path.
	//
	// See DESIGN-vault-integration.md Decision 7.
	Path string

	// Key is the lookup key within the provider. Treated as non-
	// secret: it is the path, not the stored value.
	Key string

	// Optional is true when the URI ended in ?required=false,
	// instructing the resolver to downgrade a missing key to an
	// empty value rather than failing the apply.
	Optional bool
}

// VersionToken identifies a specific revision of a resolved secret.
// The token survives into state.json so niwa status can distinguish
// user-edited drift from upstream rotation.
type VersionToken struct {
	// Token is the provider-opaque version identifier. For Infisical
	// this is the secret-version ID; for sops it is the git blob
	// hash; for the fake backend it is a deterministic SHA-256 of
	// the fixture bytes. Callers MUST treat Token as opaque.
	Token string

	// Provenance is a user-facing pointer describing where to look
	// when investigating a rotation. For Infisical this is an audit-
	// log URL; for sops it is a git commit SHA; for the fake backend
	// it identifies the fixture. May be empty when the backend
	// exposes no human-readable reference.
	Provenance string
}

// BatchResult is one entry in the slice returned by
// BatchResolver.ResolveBatch. Ref matches the corresponding input
// ref; Err is set to ErrKeyNotFound (or another error) when the
// lookup failed, and Value/Token are the resolved values otherwise.
type BatchResult struct {
	Ref   Ref
	Value secret.Value
	Token VersionToken
	Err   error
}

// ProviderConfig is the opaque configuration blob passed to
// Factory.Open. Its concrete shape is backend-specific; the resolver
// stage builds it from the parsed TOML config.
//
// Using map[string]any (rather than a typed struct per backend) lets
// niwa's config layer stay decoupled from the set of backends
// compiled in — registering a new backend does not require a config-
// schema change.
type ProviderConfig map[string]any

// ProviderSpec describes one provider as the config layer sees it:
// a name (the user-facing handle), a kind (which backend to open),
// the backend-specific config blob, and the source file path that
// declared it. Source is kept for error-message attribution only; it
// is never compared for equality.
type ProviderSpec struct {
	// Name is the user-facing provider handle. Empty for the
	// anonymous singular [vault.provider] shape.
	Name string

	// Kind is the backend kind, matching Factory.Kind() of the
	// Factory registered with the Registry.
	Kind string

	// Config is the typed config blob passed to Factory.Open.
	Config ProviderConfig

	// Source is the file path that declared this provider, used in
	// error messages for user orientation (e.g., "provider 'team'
	// declared in /path/to/niwa.toml").
	Source string
}

// Identity names the store of values one reference resolves from: the
// backend kind, the API domain, the project, the environment and the
// effective folder path. Two references with the same Identity read
// the same folder of the same project. None of the fields is secret.
//
// Several places list these fields by hand, and each must change when a
// field is added: store.fileStem (the hashed canonical form and the
// identity echo in the data file), storefallback.noticeIdentity and
// storefallback.lessIdentity, fallbacknotice.Identity (which mirrors
// this struct) with fallbacknotice's lessIdentity and fields helpers,
// the fake provider's "identity" config, and the infisical
// provider's StoreIdentity. TestFileStemCoversEveryIdentityField,
// TestNoticeIdentityCopiesEveryField, TestLessIdentityOrdersByEveryField
// (storefallback) and TestLessIdentityCoversEveryVaultIdentityField
// (fallbacknotice) fail when a new field is missing from the store key,
// the notice copy, fallbacknotice.Identity or either ordering.
type Identity struct {
	Kind        string
	APIDomain   string
	ProjectID   string
	Environment string
	FolderPath  string
}

// StoreIdentifier is an optional Provider extension for backends whose
// resolved values can be remembered per Identity. ok is false when the
// provider cannot name one for ref.
type StoreIdentifier interface {
	StoreIdentity(ref Ref) (id Identity, ok bool)
}

// NormalizeIdentity returns id in the one canonical form every
// consumer compares and hashes: the API domain as lower-cased scheme
// and host (no userinfo, path or trailing slash), and the folder path
// with a leading "/" and no trailing "/". It is the only normalisation
// of an Identity in niwa, so a provider's StoreIdentity and anything
// keyed on its result always agree.
func NormalizeIdentity(id Identity) Identity {
	id.APIDomain = normalizeAPIDomain(id.APIDomain)
	id.FolderPath = normalizeFolderPath(id.FolderPath)
	return id
}

// normalizeAPIDomain reduces a URL to its lower-cased scheme and host.
// A value that doesn't parse as an absolute URL is lower-cased and
// stripped of trailing slashes and of anything up to an "@", so
// credentials written into a malformed URL never reach a message or a
// file name.
func normalizeAPIDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if i := strings.LastIndex(raw, "@"); i >= 0 {
			raw = raw[i+1:]
		}
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// normalizeFolderPath gives p a leading "/" and drops trailing ones.
// The empty path is the root, "/".
func normalizeFolderPath(p string) string {
	return "/" + strings.Trim(strings.TrimSpace(p), "/")
}
