package lsp

import (
	"os"
	"path/filepath"
	"testing"

	powernapconfig "github.com/charmbracelet/x/powernap/pkg/config"
	"github.com/stretchr/testify/require"
)

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
