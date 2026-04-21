package config

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/env"
	"github.com/charmbracelet/crush/internal/oauth"
	openaioauth "github.com/charmbracelet/crush/internal/oauth/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type tokenRequesterFunc func(*http.Request) (*http.Response, error)

func (f tokenRequesterFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSetupOpenAICodex(t *testing.T) {
	t.Parallel()

	pc := &ProviderConfig{}
	pc.SetupOpenAICodex("test-account-id")

	require.NotNil(t, pc.ExtraHeaders)
	require.Equal(t, "test-account-id", pc.ExtraHeaders["chatgpt-account-id"])
	require.Equal(t, "responses=experimental", pc.ExtraHeaders["OpenAI-Beta"])
	require.Equal(t, "codex_cli_rs", pc.ExtraHeaders["originator"])

	require.NotNil(t, pc.ExtraBody)
	require.Equal(t, false, pc.ExtraBody["store"])
	require.Equal(t, "You are a helpful coding assistant.", pc.ExtraBody["instructions"])
	require.Contains(t, pc.ExtraBody["include"], "reasoning.encrypted_content")
}

func TestSetProviderAPIKeyOpenAICodexPersistsOAuthDefaults(t *testing.T) {
	t.Parallel()

	store, configPath := newOpenAICodexStoreForTest(t)
	token := &oauth.Token{
		AccessToken:  jwtWithAccountID("initial-account-id"),
		RefreshToken: "refresh-token",
		ExpiresIn:    3600,
	}

	err := store.SetProviderAPIKey(ScopeGlobal, OpenAICodexProviderID, token)
	require.NoError(t, err)

	provider, ok := store.Config().Providers.Get(OpenAICodexProviderID)
	require.True(t, ok)
	require.Equal(t, token.AccessToken, provider.APIKey)
	require.NotNil(t, provider.OAuthToken)
	require.Equal(t, "initial-account-id", provider.ExtraHeaders[openaioauth.HeaderAccountID])
	require.Equal(t, openaioauth.HeaderOpenAIBetaVal, provider.ExtraHeaders[openaioauth.HeaderOpenAIBeta])
	require.Equal(t, openaioauth.HeaderOriginatorVal, provider.ExtraHeaders[openaioauth.HeaderOriginator])
	require.Equal(t, false, provider.ExtraBody["store"])
	require.Equal(t, openAICodexInstructions, provider.ExtraBody["instructions"])
	require.Contains(t, provider.ExtraBody["include"], "reasoning.encrypted_content")

	data := readConfigFile(t, configPath)
	require.Equal(t, token.AccessToken, gjson.Get(data, "providers.openai-codex.api_key").String())
	require.Equal(t, token.RefreshToken, gjson.Get(data, "providers.openai-codex.oauth.refresh_token").String())
	require.Equal(t, "initial-account-id", gjson.Get(data, "providers.openai-codex.extra_headers.chatgpt-account-id").String())
	require.Equal(t, openaioauth.HeaderOpenAIBetaVal, gjson.Get(data, "providers.openai-codex.extra_headers.OpenAI-Beta").String())
	require.Equal(t, openaioauth.HeaderOriginatorVal, gjson.Get(data, "providers.openai-codex.extra_headers.originator").String())
	require.False(t, gjson.Get(data, "providers.openai-codex.extra_body.store").Bool())
	require.Equal(t, openAICodexInstructions, gjson.Get(data, "providers.openai-codex.extra_body.instructions").String())
	require.Contains(t, gjson.Get(data, "providers.openai-codex.extra_body.include").Array()[0].String(), "reasoning.encrypted_content")
}

func TestRefreshOAuthTokenOpenAICodexPreservesDefaults(t *testing.T) {
	store, configPath := newOpenAICodexStoreForTest(t)
	initialToken := &oauth.Token{
		AccessToken:  jwtWithAccountID("initial-account-id"),
		RefreshToken: "refresh-token",
		ExpiresIn:    3600,
	}

	err := store.SetProviderAPIKey(ScopeGlobal, OpenAICodexProviderID, initialToken)
	require.NoError(t, err)

	restore := openaioauth.SetTokenRequesterForTest(tokenRequesterFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		values, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "refresh_token", values.Get("grant_type"))
		require.Equal(t, "refresh-token", values.Get("refresh_token"))

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"` + jwtWithAccountID("refreshed-account-id") + `",
				"refresh_token":"refreshed-refresh-token",
				"expires_in":7200
			}`)),
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
		}, nil
	}))
	defer restore()

	err = store.RefreshOAuthToken(context.Background(), ScopeGlobal, OpenAICodexProviderID)
	require.NoError(t, err)

	provider, ok := store.Config().Providers.Get(OpenAICodexProviderID)
	require.True(t, ok)
	require.Equal(t, jwtWithAccountID("refreshed-account-id"), provider.APIKey)
	require.NotNil(t, provider.OAuthToken)
	require.Equal(t, "refreshed-refresh-token", provider.OAuthToken.RefreshToken)
	require.Equal(t, "refreshed-account-id", provider.ExtraHeaders[openaioauth.HeaderAccountID])
	require.Equal(t, openaioauth.HeaderOpenAIBetaVal, provider.ExtraHeaders[openaioauth.HeaderOpenAIBeta])
	require.Equal(t, openaioauth.HeaderOriginatorVal, provider.ExtraHeaders[openaioauth.HeaderOriginator])
	require.Equal(t, false, provider.ExtraBody["store"])
	require.Equal(t, openAICodexInstructions, provider.ExtraBody["instructions"])
	require.Contains(t, provider.ExtraBody["include"], "reasoning.encrypted_content")

	data := readConfigFile(t, configPath)
	require.Equal(t, jwtWithAccountID("refreshed-account-id"), gjson.Get(data, "providers.openai-codex.api_key").String())
	require.Equal(t, "refreshed-refresh-token", gjson.Get(data, "providers.openai-codex.oauth.refresh_token").String())
	require.Equal(t, "refreshed-account-id", gjson.Get(data, "providers.openai-codex.extra_headers.chatgpt-account-id").String())
	require.Equal(t, openaioauth.HeaderOpenAIBetaVal, gjson.Get(data, "providers.openai-codex.extra_headers.OpenAI-Beta").String())
	require.Equal(t, openaioauth.HeaderOriginatorVal, gjson.Get(data, "providers.openai-codex.extra_headers.originator").String())
	require.False(t, gjson.Get(data, "providers.openai-codex.extra_body.store").Bool())
	require.Equal(t, openAICodexInstructions, gjson.Get(data, "providers.openai-codex.extra_body.instructions").String())
	require.Contains(t, gjson.Get(data, "providers.openai-codex.extra_body.include").Array()[0].String(), "reasoning.encrypted_content")
}

func TestOpenAICodexProviderIncludesLatestModels(t *testing.T) {
	t.Parallel()

	provider := OpenAICodexProvider()

	require.Equal(t, "gpt-5.2-codex", provider.DefaultLargeModelID)
	require.Equal(t, "gpt-5.1-codex-mini", provider.DefaultSmallModelID)
	require.Contains(t, modelIDs(provider.Models), "gpt-5.4")
	require.Contains(t, modelIDs(provider.Models), "gpt-5.4-mini")
	require.Contains(t, modelIDs(provider.Models), "gpt-5.4-nano")
	require.Contains(t, modelIDs(provider.Models), "gpt-5.3-codex")

	gpt54 := modelByID(t, provider.Models, "gpt-5.4")
	require.EqualValues(t, 1050000, gpt54.ContextWindow)
	require.EqualValues(t, 128000, gpt54.DefaultMaxTokens)
	require.Equal(t, []string{"none", "low", "medium", "high", "xhigh"}, gpt54.ReasoningLevels)

	gpt53Codex := modelByID(t, provider.Models, "gpt-5.3-codex")
	require.EqualValues(t, 400000, gpt53Codex.ContextWindow)
	require.Equal(t, []string{"low", "medium", "high", "xhigh"}, gpt53Codex.ReasoningLevels)
}

func TestConfigConfigureProvidersOpenAICodexAddsDefaults(t *testing.T) {
	t.Parallel()

	knownProviders := []catwalk.Provider{OpenAICodexProvider()}
	cfg := &Config{
		Providers: csync.NewMapFrom(map[string]ProviderConfig{
			OpenAICodexProviderID: {
				APIKey: "test-key",
			},
		}),
	}
	cfg.setDefaults(t.TempDir(), "")

	envv := env.NewFromMap(map[string]string{})
	resolver := NewEnvironmentVariableResolver(envv)
	err := cfg.configureProviders(testStore(cfg), envv, resolver, knownProviders)
	require.NoError(t, err)

	provider, ok := cfg.Providers.Get(OpenAICodexProviderID)
	require.True(t, ok)
	require.Equal(t, "test-key", provider.APIKey)
	require.NotContains(t, provider.ExtraHeaders, "chatgpt-account-id")
	require.Equal(t, "responses=experimental", provider.ExtraHeaders["OpenAI-Beta"])
	require.Equal(t, "codex_cli_rs", provider.ExtraHeaders["originator"])
	require.Equal(t, false, provider.ExtraBody["store"])
	require.Equal(t, "You are a helpful coding assistant.", provider.ExtraBody["instructions"])
}

func newOpenAICodexStoreForTest(t *testing.T) (*ConfigStore, string) {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := &Config{
		Providers: csync.NewMap[string, ProviderConfig](),
	}
	cfg.setDefaults(tmpDir, "")

	configPath := filepath.Join(tmpDir, "crush.json")
	store := &ConfigStore{
		config:             cfg,
		globalDataPath:     configPath,
		knownProviders:     []catwalk.Provider{OpenAICodexProvider()},
		autoReloadDisabled: true,
	}
	return store, configPath
}

func jwtWithAccountID(accountID string) string {
	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func readConfigFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func modelIDs(models []catwalk.Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func modelByID(t *testing.T, models []catwalk.Model, id string) catwalk.Model {
	t.Helper()

	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("model %q not found", id)
	return catwalk.Model{}
}
