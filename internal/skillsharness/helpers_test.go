package skillsharness_test

import (
	"path/filepath"
	"runtime"
)

// packageDir returns the absolute path of the skillsharness package directory.
// Used by localhost_guard_test.go to locate the transcripts sub-directory
// regardless of the working directory at test time.
func packageDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}
