package dialog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOAuthOpenAIOpenURLReinitializesWhenURLMissing(t *testing.T) {
	t.Parallel()

	dialog := &OAuthOpenAI{}

	cmd := dialog.openURL()
	require.NotNil(t, cmd)

	msg := cmd()
	initMsg, ok := msg.(openAICodexAuthInitMsg)
	require.True(t, ok)
	require.NotEmpty(t, initMsg.flow.URL)
	require.Equal(t, openAIAuthInitActionOpenBrowser, initMsg.afterInit)
}

func TestOAuthOpenAICopyURLReinitializesWhenURLMissing(t *testing.T) {
	t.Parallel()

	dialog := &OAuthOpenAI{}

	cmd := dialog.copyURL()
	require.NotNil(t, cmd)

	msg := cmd()
	initMsg, ok := msg.(openAICodexAuthInitMsg)
	require.True(t, ok)
	require.NotEmpty(t, initMsg.flow.URL)
	require.Equal(t, openAIAuthInitActionCopyURL, initMsg.afterInit)
}
