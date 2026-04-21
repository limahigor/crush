package lsp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/csync"
	powernapconfig "github.com/charmbracelet/x/powernap/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestUnavailableBackoff(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)
	now := base

	manager := &Manager{
		unavailable: csync.NewMap[string, time.Time](),
		now:         func() time.Time { return now },
	}

	require.False(t, manager.recentlyUnavailable("gopls"))

	manager.markUnavailable("gopls")
	require.True(t, manager.recentlyUnavailable("gopls"))

	now = now.Add(unavailableRetryDelay + time.Second)
	require.False(t, manager.recentlyUnavailable("gopls"))
	_, exists := manager.unavailable.Get("gopls")
	require.False(t, exists)

	manager.markUnavailable("gopls")
	manager.clearUnavailable("gopls")
	require.False(t, manager.recentlyUnavailable("gopls"))
}

func TestHandlesWorkspaceDirectory(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	goMod := filepath.Join(workDir, "go.mod")
	require.NoError(t, os.WriteFile(goMod, []byte("module example.com/test\n"), 0o644))

	server := &powernapconfig.ServerConfig{
		Command:     "gopls",
		FileTypes:   []string{"go"},
		RootMarkers: []string{"go.mod"},
	}

	require.True(t, handles(server, workDir, workDir))
}

func TestHandlesWorkspaceDirectoryRequiresRootMarkers(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	server := &powernapconfig.ServerConfig{
		Command:     "gopls",
		FileTypes:   []string{"go"},
		RootMarkers: []string{"go.mod"},
	}

	require.False(t, handles(server, workDir, workDir))
}
