package cli

import (
	"encoding/json"
	"fmt"
)

// renderLaunchSettings renders the inline settings document a dispatch passes
// to the launched agent's settings flag, and reports whether there is one.
//
// A launch has exactly one settings slot: a repeated settings flag is
// last-wins and silent, so a second document would quietly drop the first.
// Every contributor therefore adds its key to one map that step (9c) of
// runDispatch builds, and the map is rendered here once. Each contributor
// decides for itself whether its key goes in; rendering says nothing about
// which of them did, so a caller that needs to know whether its own key was
// sent must track that from its own decision, not from this result.
//
// The encoding is encoding/json's, which sorts map keys, so two contributors
// always produce the same bytes in the same order. With remote control's key
// alone the output is byte-identical to remoteControlSettingsJSON.
//
// Contributors must pass constant keys and values only -- never workspace,
// repository, or prompt input. The document outranks the instance's own
// settings files, so anything a caller could influence here would let that
// caller rewrite the worker's configuration.
//
// An empty or nil map returns ("", false), and the caller then appends no
// settings flag at all. A value encoding/json can't encode panics, since only
// a contributor breaking the constants rule can cause one.
func renderLaunchSettings(settings map[string]any) (string, bool) {
	if len(settings) == 0 {
		return "", false
	}
	doc, err := json.Marshal(settings)
	if err != nil {
		// The string, boolean, and number constants contributors pass always
		// encode, so reaching this is a programming error in a contributor. It panics rather than returning
		// no document: a contributor records its own decision (rcInjected, for
		// one), and a silent drop would leave that record saying its key was
		// sent when it wasn't.
		panic(fmt.Sprintf("renderLaunchSettings: encoding launch settings: %v", err))
	}
	return string(doc), true
}
