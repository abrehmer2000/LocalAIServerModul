package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RuntimeEnvironment makes bundled shared libraries discoverable without
// modifying system-wide loader configuration. Preserve accelerator/driver paths.
func RuntimeEnvironment(binary string) []string {
	key := "LD_LIBRARY_PATH"
	if runtime.GOOS == "windows" {
		key = "PATH"
	} else if runtime.GOOS == "darwin" {
		key = "DYLD_LIBRARY_PATH"
	}
	prefix := filepath.Dir(binary)
	env := os.Environ()
	for i, value := range env {
		parts := strings.SplitN(value, "=", 2)
		if strings.EqualFold(parts[0], key) {
			env[i] = key + "=" + prefix + string(os.PathListSeparator) + parts[1]
			return env
		}
	}
	return append(env, key+"="+prefix)
}
