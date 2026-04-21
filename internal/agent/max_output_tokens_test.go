package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func TestMaxOutputTokensForProvider(t *testing.T) {
	t.Parallel()

	t.Run("openai codex omits max output tokens", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, maxOutputTokensForProvider(config.OpenAICodexProviderID, 128000))
	})

	t.Run("other providers keep positive max output tokens", func(t *testing.T) {
		t.Parallel()

		got := maxOutputTokensForProvider("openai", 4096)
		require.NotNil(t, got)
		require.Equal(t, int64(4096), *got)
	})

	t.Run("non-positive values are omitted", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, maxOutputTokensForProvider("openai", 0))
		require.Nil(t, maxOutputTokensForProvider("openai", -1))
	})
}
