//go:build !linux && !darwin

package workspace

// renameNoReplace has no one-step rename that refuses to replace on this
// platform, so it checks and renames in two steps.
func renameNoReplace(from, to string) error {
	return renameChecked(from, to)
}
