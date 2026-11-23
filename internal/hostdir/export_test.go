package hostdir

// SetBeforeRead sets the strict walk's beforeRead hook for a test and returns
// the func that clears it.
func SetBeforeRead(hook func(dir string)) (restore func()) {
	beforeRead = hook
	return func() { beforeRead = nil }
}
