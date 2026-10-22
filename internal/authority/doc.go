// Package authority holds the rules for authority levels and named grants:
// the level and authority vocabularies, the policy the three config layers
// declare, how a dispatch request resolves against that policy, the authority
// record's JSON encoding and its reader, and the decision-point answer.
//
// It is a pure leaf package. It does no I/O and imports nothing else from
// niwa, so dispatch, the authority commands, the config loaders and niwa list
// all share one copy of the rules, and every rule can be tested without a
// filesystem or a command tree.
//
// It also owns the formats of the two ids a record carries: the calling
// session's id (ValidSessionID) and the dispatch id (ValidDispatchID). The
// lineage code and niwa list check ids through the same functions, so the
// record's reader and the code that writes the ids can't drift apart.
package authority
