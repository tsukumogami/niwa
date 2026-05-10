package sessionattach

import (
	"os"
	"strconv"
)

// writeFile is a tiny helper used by the supervise tests' fakeBin. Pulled out
// so the test file can stay focused on assertions.
func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}

func chmod(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
