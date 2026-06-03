package browserutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenURLDoesNotWaitForBrowserExit(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "freebsd", "netbsd", "openbsd":
	default:
		t.Skip("provider lookup test only applies to Unix browser launchers")
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "xdg-open")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 2\n"), 0o755)
	require.NoError(t, err)

	t.Setenv("PATH", dir)

	start := time.Now()
	err = OpenURL("https://example.com")
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Less(t, elapsed, 500*time.Millisecond)
}
