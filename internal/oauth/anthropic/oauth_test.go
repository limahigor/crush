package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type testTokenRequester func(*http.Request) (*http.Response, error)

func (f testTokenRequester) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCreateAuthorizationFlow(t *testing.T) {
	t.Parallel()

	flow, err := CreateAuthorizationFlow()
	require.NoError(t, err)
	require.NotEmpty(t, flow.URL)
	require.NotEmpty(t, flow.State)
	require.NotEmpty(t, flow.Verifier)

	parsed, err := url.Parse(flow.URL)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "claude.com", parsed.Host)
	require.Equal(t, "/cai/oauth/authorize", parsed.Path)

	query := parsed.Query()
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, oauthClientID, query.Get("client_id"))
	require.Equal(t, RedirectURL, query.Get("redirect_uri"))
	require.Equal(t, Scope, query.Get("scope"))
	require.NotEmpty(t, query.Get("code_challenge"))
	require.Equal(t, codeChallenge(flow.Verifier), query.Get("code_challenge"))
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.Equal(t, flow.State, query.Get("state"))
	require.Equal(t, "true", query.Get("code"))
}

func TestExchangeAuthorizationCode(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, tokenURL, req.URL.String())
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		require.Equal(t, tokenAccept, req.Header.Get("Accept"))
		require.Equal(t, tokenUserAgent, req.Header.Get("User-Agent"))

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "authorization_code", payload["grant_type"])
		require.Equal(t, oauthClientID, payload["client_id"])
		require.Equal(t, "test-code", payload["code"])
		require.Equal(t, "flow-state", payload["state"])
		require.Equal(t, "test-verifier", payload["code_verifier"])
		require.Equal(t, RedirectURL, payload["redirect_uri"])

		return jsonResponse(http.StatusOK, `{
			"access_token":"test-access-token",
			"refresh_token":"test-refresh-token",
			"expires_in":3600
		}`), nil
	}))
	defer restore()

	token, err := ExchangeAuthorizationCode(context.Background(), "test-code", "flow-state", "test-verifier")
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, "test-access-token", token.AccessToken)
	require.Equal(t, "test-refresh-token", token.RefreshToken)
	require.Equal(t, 3600, token.ExpiresIn)
}

func TestExchangeAuthorizationCodeIncludesStateWhenPresent(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "test-code", payload["code"])
		require.Equal(t, "test-state", payload["state"])

		return jsonResponse(http.StatusOK, `{
			"access_token":"test-access-token",
			"refresh_token":"test-refresh-token",
			"expires_in":3600
		}`), nil
	}))
	defer restore()

	token, err := ExchangeAuthorizationCode(context.Background(), "https://platform.claude.com/oauth/code/callback?code=test-code&state=test-state", "flow-state", "test-verifier")
	require.NoError(t, err)
	require.NotNil(t, token)
}

func TestRefreshToken(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		require.Equal(t, tokenAccept, req.Header.Get("Accept"))
		require.Equal(t, tokenUserAgent, req.Header.Get("User-Agent"))

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "refresh_token", payload["grant_type"])
		require.Equal(t, oauthClientID, payload["client_id"])
		require.Equal(t, "test-refresh-token", payload["refresh_token"])

		return jsonResponse(http.StatusOK, `{
			"access_token":"new-access-token",
			"refresh_token":"new-refresh-token",
			"expires_in":3600
		}`), nil
	}))
	defer restore()

	token, err := RefreshToken(context.Background(), "test-refresh-token")
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, "new-access-token", token.AccessToken)
	require.Equal(t, "new-refresh-token", token.RefreshToken)
}

func TestParseAuthorizationInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
	}{
		{
			name:      "full redirect url",
			input:     "https://platform.claude.com/oauth/code/callback?code=test-code&state=test-state",
			wantCode:  "test-code",
			wantState: "test-state",
		},
		{
			name:      "query string",
			input:     "code=test-code&state=test-state",
			wantCode:  "test-code",
			wantState: "test-state",
		},
		{
			name:      "code and state fragment",
			input:     "test-code#test-state",
			wantCode:  "test-code",
			wantState: "test-state",
		},
		{
			name:     "raw code",
			input:    "test-code",
			wantCode: "test-code",
		},
		{
			name:  "empty",
			input: "   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, state := ParseAuthorizationInput(tt.input)
			require.Equal(t, tt.wantCode, code)
			require.Equal(t, tt.wantState, state)
		})
	}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}
}
