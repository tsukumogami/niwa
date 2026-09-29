package storefallback

import (
	"context"
	"errors"

	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// Wrap returns p wrapped in the session's store fallback when p
// implements vault.StoreIdentifier, and p itself otherwise. It decides
// by the type alone; whether a given reference has an identity is asked
// per call, inside Resolve. Its signature fits vault.Bundle.Wrap.
func (s *Session) Wrap(p vault.Provider) vault.Provider {
	ider, ok := p.(vault.StoreIdentifier)
	if !ok {
		return p
	}
	return &provider{inner: p, ider: ider, s: s}
}

// provider is the store-fallback decorator. It implements only
// vault.Provider, never vault.BatchResolver, so every reference goes
// through Resolve and its policy.
type provider struct {
	inner vault.Provider
	ider  vault.StoreIdentifier
	s     *Session
}

func (w *provider) Name() string { return w.inner.Name() }

func (w *provider) Kind() string { return w.inner.Kind() }

func (w *provider) Close() error { return w.inner.Close() }

// Resolve makes exactly one inner Resolve call and applies policy to its
// result. Every path except a served value returns the inner result
// unchanged, down to the identical error value.
func (w *provider) Resolve(ctx context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	id, ok := w.ider.StoreIdentity(ref)
	if !ok {
		return w.inner.Resolve(ctx, ref)
	}
	id = vault.NormalizeIdentity(id)

	val, token, err := w.inner.Resolve(ctx, ref)
	d := policy(err)
	switch d.action {
	case actionRecord:
		w.s.put(id, ref.Key, store.Entry{
			Value:        append([]byte(nil), reveal.UnsafeReveal(val)...),
			ResolvedAt:   w.s.now(),
			VersionToken: token.Token,
			Provenance:   token.Provenance,
		})
	case actionEvictKey:
		w.s.evictKey(id, ref.Key)
	case actionEvictAll:
		w.s.evictAll(id)
	case actionFallBack:
		entry, stored := w.s.lookup(id, ref.Key)
		if !stored {
			w.s.notices.NothingToFallBackOn(noticeIdentity(id))
			break
		}
		w.s.notices.Served(noticeIdentity(id), d.reason, entry.ResolvedAt)
		served := secret.New(entry.Value, secret.Origin{
			ProviderName: w.inner.Name(),
			Key:          ref.Key,
			VersionToken: entry.VersionToken,
		})
		return served, vault.VersionToken{Token: entry.VersionToken, Provenance: entry.Provenance}, nil
	}
	return val, token, err
}

// action is what the decorator does with one inner result.
type action int

const (
	// actionPass returns the inner result and records nothing.
	actionPass action = iota
	// actionRecord buffers the resolved value for the store.
	actionRecord
	// actionEvictKey buffers the removal of the requested key.
	actionEvictKey
	// actionEvictAll buffers the removal of every key of the identity.
	actionEvictAll
	// actionFallBack serves the stored value, when there is one.
	actionFallBack
)

type decision struct {
	action action
	// reason is set for actionFallBack.
	reason fallbacknotice.Reason
}

// policy decides what to do with the error an inner Resolve returned.
// It depends on nothing but err.
//
//   - nil: record the value.
//   - vault.ErrKeyNotFound: evict the key.
//   - answered with HTTP 403 or 404: evict the whole identity, since the
//     answer applies to the folder.
//   - unauthenticated or unreachable, whatever the reason: fall back.
//   - anything else, including vault.ErrClientNotInstalled and an
//     unclassified vault.ErrProviderUnreachable: pass through.
func policy(err error) decision {
	if err == nil {
		return decision{action: actionRecord}
	}
	if errors.Is(err, vault.ErrKeyNotFound) {
		return decision{action: actionEvictKey}
	}
	var class *vault.FailureClass
	if !errors.As(err, &class) {
		return decision{action: actionPass}
	}
	switch class.Class {
	case vault.ClassAnswered:
		if class.HTTPStatus == 403 || class.HTTPStatus == 404 {
			return decision{action: actionEvictAll}
		}
	case vault.ClassUnauthenticated, vault.ClassUnreachable:
		return decision{action: actionFallBack, reason: noticeReason(*class)}
	}
	return decision{action: actionPass}
}

// noticeReason maps a failure's vault.Reason one to one onto the
// collector's reasons. A class that arrives without a reason gets the
// one its class implies.
func noticeReason(c vault.FailureClass) fallbacknotice.Reason {
	switch c.Reason {
	case vault.ReasonLoggedOut:
		return fallbacknotice.ReasonLoggedOut
	case vault.ReasonTimedOut:
		return fallbacknotice.ReasonTimedOut
	case vault.ReasonUnreachable:
		return fallbacknotice.ReasonUnreachable
	}
	if c.Class == vault.ClassUnauthenticated {
		return fallbacknotice.ReasonLoggedOut
	}
	return fallbacknotice.ReasonUnreachable
}

// noticeIdentity copies a vault.Identity into the collector's own type.
func noticeIdentity(id vault.Identity) fallbacknotice.Identity {
	return fallbacknotice.Identity{
		Kind:        id.Kind,
		APIDomain:   id.APIDomain,
		ProjectID:   id.ProjectID,
		Environment: id.Environment,
		FolderPath:  id.FolderPath,
	}
}
