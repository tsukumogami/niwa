// Package infisical serves two distinct, deliberately separate
// purposes against the Infisical secrets manager.
//
// The first is the v1 Infisical vault backend for niwa's ordinary
// secret resolution: it shells out to the user-installed `infisical`
// CLI (R20 — no Go SDK dependency) and exposes both vault.Provider and
// the optional vault.BatchResolver interfaces (this file, subprocess.go).
// bounds.go puts a deadline and an output cap on every CLI call on that
// path, and classify.go classifies a failed export (unauthenticated,
// unreachable or answered) from its server response and an
// `infisical login status` probe, which is what decides whether the
// store fallback may serve stored values for it.
//
// The second, added for `niwa onboard` (DESIGN-niwa-onboard.md
// Decision 4), is a net-new Universal Auth identity management REST
// client plus session/org detection (management.go, session.go): read
// an identity's client_id, mint or revoke a client secret, and read an
// environment as the R9 two-hop mint-time verification proof. Both
// purposes authenticate as the operator's own live session — never an
// admin token niwa custodies — and NEITHER management.go's functions
// nor session.go's DetectSessionStatus are reachable through
// vault.Provider, vault.BatchResolver, or vault.DefaultRegistry: they
// are plain package-level functions the onboard wizard calls directly.
// vault.Provider is deliberately read-shaped and must never bend into
// a management interface, so this second purpose is homed here
// specifically because it shares no interface surface with the first —
// only the wire-format knowledge (REST JSON shapes, CLI argv shapes,
// the redaction/scrubbing discipline) that makes this package the
// single place niwa speaks every dialect of "talking to Infisical."
//
// The vault-backend half is lazy: Factory.Open does NOT invoke any
// subprocess. The first call to Resolve or ResolveBatch triggers a
// single `infisical export --format json` invocation per (project,
// env, path) triple; results are cached in-process for the lifetime of
// the Provider. Close clears the cache.
//
// Auth model (vault-backend half): niwa does NOT attempt to
// authenticate. The Infisical CLI reads its own credentials
// (`INFISICAL_TOKEN`, the `~/.infisical` config the user creates via
// `infisical login`, etc.). niwa passes `cmd.Env = nil` to inherit the
// parent environment unchanged — this keeps auth transparent to the
// user and avoids the anti-pattern of niwa itself handling Infisical
// tokens.
//
// Argv hygiene (R21): `--projectId`, `--env`, `--path` are passed on
// argv because they are NOT secrets (they identify, they are not
// stored values). No secret value ever reaches argv — this holds for
// the management REST client too, which carries every credential in
// an HTTP Authorization header, never on argv or in an environment
// variable.
//
// Stderr hygiene (R22): subprocess stderr is fully captured (never
// streamed to the parent process's stderr) and scrubbed through
// vault.ScrubStderr before being interpolated into any returned
// error. Errors are wrapped via secret.Errorf so subsequent re-wraps
// by callers continue to scrub any late-registered fragments. The
// management REST client applies the analogous discipline to response
// bodies: every secret.Value parameter is registered on the context's
// redactor before use, and every non-2xx response body is scrubbed
// before being interpolated into an error.
//
// Registration: this package's init() registers a Factory with
// vault.DefaultRegistry. Production code (any caller that looks up
// via DefaultRegistry) can open Infisical providers without an
// explicit import of this package — but the binary must import it
// somewhere so init() runs. The niwa main package does so. This
// registration covers ONLY the vault-backend half; the management
// surface has no registry entry and no factory — it is reached only
// by a direct import and a direct function call, which is what makes
// the AC-10 static call-site check in lint_test.go meaningful.
package infisical

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// Kind is the factory kind registered with vault.Registry.
const Kind = "infisical"

// defaultEnv is the Infisical environment slug assumed when the
// provider config does not supply one.
const defaultEnv = "dev"

// defaultPath is the Infisical folder path assumed when the provider
// config does not supply one. The root folder is "/".
const defaultPath = "/"

// Factory is the vault.Factory implementation for the Infisical
// backend. Factory is stateless: every Open call constructs a fresh
// Provider.
type Factory struct{}

// NewFactory returns a ready-to-register Factory. Callers typically
// do not need this — the package's init() registers a shared
// instance with vault.DefaultRegistry.
func NewFactory() *Factory {
	return &Factory{}
}

// Kind returns the backend kind string ("infisical").
func (Factory) Kind() string {
	return Kind
}

// Open constructs a Provider from the supplied configuration.
//
// Recognised keys:
//
//	"project"    string     // REQUIRED. Infisical project ID.
//	"env"        string     // optional. Environment slug, default "dev".
//	"path"       string     // optional. Folder path inside the project, default "/".
//	"name"       string     // optional. Provider handle for Registry bookkeeping.
//	"api_url"    string     // optional. Read only to name the store identity's API domain.
//	"token"      string     // optional. A token niwa minted; passed to export via --token.
//	"_commander" commander  // test-only. Swaps the subprocess runner for a fake.
//
// Unknown keys are ignored — forward compatibility for future
// config knobs. Malformed types for recognised keys cause Open to
// return an error.
//
// Open is non-blocking: it does NOT invoke `infisical`. The first
// Resolve / ResolveBatch triggers the subprocess (see Provider doc).
func (Factory) Open(_ context.Context, config vault.ProviderConfig) (vault.Provider, error) {
	p := &Provider{
		env:       defaultEnv,
		path:      defaultPath,
		commander: defaultCommander{},
	}

	rawProject, ok := config["project"]
	if !ok {
		return nil, fmt.Errorf("infisical: config[project] is required")
	}
	project, ok := rawProject.(string)
	if !ok {
		return nil, fmt.Errorf("infisical: config[project] must be string, got %T", rawProject)
	}
	if project == "" {
		return nil, fmt.Errorf("infisical: config[project] must be non-empty")
	}
	p.project = project

	if raw, ok := config["env"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("infisical: config[env] must be string, got %T", raw)
		}
		if s != "" {
			p.env = s
		}
	}

	if raw, ok := config["path"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("infisical: config[path] must be string, got %T", raw)
		}
		if s != "" {
			p.path = s
		}
	}

	if raw, ok := config["name"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("infisical: config[name] must be string, got %T", raw)
		}
		p.name = s
	}

	// Optional per-provider auth token. When present, passed via --token
	// to infisical export, bypassing the CLI's stored session. Used for
	// multi-org scenarios where the CLI session is scoped to a different
	// org than this provider's project. Injected by apply.go from the
	// local credential file (~/.config/niwa/provider-auth.toml).
	if raw, ok := config["token"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("infisical: config[token] must be string, got %T", raw)
		}
		p.token = s
	}

	// The API URL, with the precedence niwa's own Infisical calls use,
	// only to name the provider's store identity and the domain the
	// run state keys on. Nothing new is passed to the CLI, which keeps
	// finding its server the way it always has.
	var configAPIURL string
	if raw, ok := config["api_url"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("infisical: config[api_url] must be string, got %T", raw)
		}
		configAPIURL = s
	}
	p.apiDomain = vault.NormalizeIdentity(vault.Identity{APIDomain: resolveAPIURL(configAPIURL)}).APIDomain

	// Test-only hook: allow a caller to inject a fake commander.
	if raw, ok := config["_commander"]; ok {
		c, ok := raw.(commander)
		if !ok {
			return nil, fmt.Errorf("infisical: config[_commander] must implement commander, got %T", raw)
		}
		p.commander = c
	}

	return p, nil
}

// Provider is the Infisical backend's vault.Provider implementation.
// It is safe for concurrent Resolve and ResolveBatch calls; the
// lazy-fetch and per-path cache are protected by a mutex.
type Provider struct {
	name    string
	project string
	env     string
	path    string // Factory.Open-time default path (used when Ref.Path is empty)
	token   string // optional JWT for multi-org auth; passed via --token to subprocess
	// apiDomain is the normalised scheme and host of the provider's
	// API URL: the store identity's domain and the run-state key. It
	// comes from niwa's configuration, not from the CLI, which picks
	// its own server; the two can differ (a CLI logged in to another
	// region with no api_url in niwa's config), so a verdict keyed on it
	// is per configured domain, not per server the CLI talked to.
	apiDomain string

	commander commander

	mu     sync.Mutex
	closed bool
	// paths caches `infisical export` results per effective folder
	// path. Each entry is loaded lazily on the first Resolve against
	// that path. Populated by ensureLoaded; cleared by Close.
	//
	// See DESIGN-vault-integration.md Decision 7 for why a single
	// Provider fetches multiple paths: Ref.Path can override p.path
	// per resolve, so one anonymous [vault.provider] can reach every
	// folder in the project.
	paths map[string]*pathCache
}

// pathCache holds one loaded path's secrets and its derived version
// token. A Provider keeps one pathCache per effective folder path.
type pathCache struct {
	values       map[string]string
	versionToken vault.VersionToken
}

// Name returns the user-facing provider name configured via
// config["name"]. Empty when the provider is the anonymous singular
// provider.
func (p *Provider) Name() string {
	return p.name
}

// displayName returns p.name, substituting "<anonymous>" when the
// provider has no configured name. Used for error-message
// interpolation so users don't see the bare `provider ""` string.
func (p *Provider) displayName() string {
	if p.name == "" {
		return "<anonymous>"
	}
	return p.name
}

// Kind returns the backend kind string ("infisical").
func (p *Provider) Kind() string {
	return Kind
}

// effectivePath returns the folder path to use for a given Ref:
// Ref.Path wins when non-empty, otherwise the provider's Open-time
// default p.path. See DESIGN-vault-integration.md Decision 7.
func (p *Provider) effectivePath(ref vault.Ref) string {
	if ref.Path != "" {
		return ref.Path
	}
	return p.path
}

// StoreIdentity implements vault.StoreIdentifier: the folder ref
// resolves from, in this provider's project, environment and API
// domain, normalised.
func (p *Provider) StoreIdentity(ref vault.Ref) (vault.Identity, bool) {
	return vault.NormalizeIdentity(vault.Identity{
		Kind:        Kind,
		APIDomain:   p.apiDomain,
		ProjectID:   p.project,
		Environment: p.env,
		FolderPath:  p.effectivePath(ref),
	}), true
}

// minted reports whether exports run with a token niwa minted from a
// machine identity, rather than the CLI's own session.
func (p *Provider) minted() bool {
	return p.token != ""
}

// Resolve fetches a single secret by key. Triggers an
// `infisical export --path <effective-path>` the first time a given
// effective path is seen; subsequent resolves against the same path
// hit the cache.
//
// Returns vault.ErrKeyNotFound when the requested key is not present
// in the exported payload. A failed export returns a
// *vault.ClassifiedError (see runInfisicalExport); it matches
// vault.ErrProviderUnreachable when classified unauthenticated or
// unreachable, when an export skipped because of an earlier verdict in
// the run, and for an answered failure whose stderr carries an
// auth-failure marker.
func (p *Provider) Resolve(ctx context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	effPath := p.effectivePath(ref)
	if err := p.ensureLoaded(ctx, effPath); err != nil {
		return secret.Value{}, vault.VersionToken{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return secret.Value{}, vault.VersionToken{}, secret.Errorf(
			"infisical: provider %q: %w", p.displayName(), vault.ErrProviderUnreachable,
		)
	}

	cache := p.paths[effPath]
	raw, ok := cache.values[ref.Key]
	if !ok {
		return secret.Value{}, vault.VersionToken{}, secret.Errorf(
			"infisical: project %q env %q path %q key %q: %w",
			p.project, p.env, effPath, ref.Key, vault.ErrKeyNotFound,
		)
	}

	origin := secret.Origin{
		ProviderName: p.name,
		Key:          ref.Key,
		VersionToken: cache.versionToken.Token,
	}
	return secret.New([]byte(raw), origin), cache.versionToken, nil
}

// ResolveBatch implements vault.BatchResolver. It groups refs by
// effective folder path and issues one `infisical export` per unique
// path (cached). Missing keys are signalled per-entry by setting
// BatchResult.Err to vault.ErrKeyNotFound; ResolveBatch never returns
// a top-level error for a partial miss.
//
// A top-level error is returned only for infrastructure failures
// (load failed, provider closed). In that case the returned slice is
// nil.
func (p *Provider) ResolveBatch(ctx context.Context, refs []vault.Ref) ([]vault.BatchResult, error) {
	// Walk refs to discover every unique effective path and load each
	// one. Loading any single path failing aborts the whole batch —
	// callers treat ResolveBatch top-level errors as infrastructure
	// failures, which matches "auth broke" or "CLI missing".
	seenPaths := make(map[string]bool, 1)
	for _, ref := range refs {
		effPath := p.effectivePath(ref)
		if seenPaths[effPath] {
			continue
		}
		seenPaths[effPath] = true
		if err := p.ensureLoaded(ctx, effPath); err != nil {
			return nil, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, secret.Errorf(
			"infisical: provider %q: %w", p.displayName(), vault.ErrProviderUnreachable,
		)
	}

	results := make([]vault.BatchResult, len(refs))
	for i, ref := range refs {
		effPath := p.effectivePath(ref)
		cache := p.paths[effPath]
		raw, ok := cache.values[ref.Key]
		if !ok {
			results[i] = vault.BatchResult{
				Ref: ref,
				Err: secret.Errorf(
					"infisical: project %q env %q path %q key %q: %w",
					p.project, p.env, effPath, ref.Key, vault.ErrKeyNotFound,
				),
			}
			continue
		}
		origin := secret.Origin{
			ProviderName: p.name,
			Key:          ref.Key,
			VersionToken: cache.versionToken.Token,
		}
		results[i] = vault.BatchResult{
			Ref:   ref,
			Value: secret.New([]byte(raw), origin),
			Token: cache.versionToken,
		}
	}
	return results, nil
}

// Close releases the in-memory cache. Subsequent Resolve /
// ResolveBatch calls return vault.ErrProviderUnreachable. Close is
// idempotent: calling it twice never returns an error.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.paths = nil
	return nil
}

// ensureLoaded triggers the `infisical export` subprocess for the
// given effective path on first call. Results are cached per path;
// later callers hit the cache without re-running the subprocess.
// Safe for concurrent callers — the mutex serialises the initial
// fetch for each path.
func (p *Provider) ensureLoaded(ctx context.Context, effPath string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return secret.Errorf(
			"infisical: provider %q: %w", p.displayName(), vault.ErrProviderUnreachable,
		)
	}
	if _, ok := p.paths[effPath]; ok {
		p.mu.Unlock()
		return nil
	}
	// Release the mutex while the subprocess runs so concurrent
	// Resolve callers do not hold the lock through network I/O. We
	// re-acquire before mutating state and re-check the cache to
	// swallow the second-caller race.
	p.mu.Unlock()

	// An earlier call in this run already found the domain unreachable,
	// or this principal logged out on it: don't start a subprocess that
	// would only fail the same way.
	runState := vault.RunStateFrom(ctx)
	if class, ok := runState.Check(p.apiDomain, p.minted()); ok {
		return skippedExportError(p.apiDomain, class)
	}

	values, token, err := runInfisicalExport(ctx, p.commander, p.project, p.env, effPath, p.token)
	if err != nil {
		var class *vault.FailureClass
		if errors.As(err, &class) {
			switch {
			case class.Class == vault.ClassUnreachable:
				runState.MarkUnreachable(p.apiDomain, class.Reason)
			case class.Class == vault.ClassUnauthenticated && !p.minted():
				runState.MarkUnauthenticated(p.apiDomain)
			}
		}
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return secret.Errorf(
			"infisical: provider %q: %w", p.displayName(), vault.ErrProviderUnreachable,
		)
	}
	if p.paths == nil {
		p.paths = map[string]*pathCache{}
	}
	if _, ok := p.paths[effPath]; !ok {
		p.paths[effPath] = &pathCache{values: values, versionToken: token}
	}
	return nil
}

// skippedExportError is returned instead of running an export that an
// earlier verdict in the run already decided. It carries that verdict's
// class and reason and matches vault.ErrProviderUnreachable.
func skippedExportError(domain string, class vault.FailureClass) error {
	what := "was unreachable"
	switch {
	case class.Class == vault.ClassUnauthenticated:
		what = "was logged out or expired"
	case class.Reason == vault.ReasonTimedOut:
		what = "timed out"
	}
	return vault.Classify(
		fmt.Errorf("infisical: export for %s skipped: an earlier call in this run %s", domain, what),
		class,
	)
}

// init registers a shared Factory with vault.DefaultRegistry so that
// production code paths can open Infisical providers without
// importing this package by name.
//
// If registration fails (e.g., a previous init already registered
// under the same Kind), init panics: a duplicate registration is a
// programming error that should surface at binary startup rather
// than silently hide an import-graph bug.
func init() {
	if err := vault.DefaultRegistry.Register(&Factory{}); err != nil {
		panic(fmt.Sprintf("infisical: registering default factory: %v", err))
	}
}
