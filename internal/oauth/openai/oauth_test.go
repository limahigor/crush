package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/oauth"
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
	require.Equal(t, "auth.openai.com", parsed.Host)
	require.Equal(t, "/oauth/authorize", parsed.Path)

	query := parsed.Query()
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, oauthClientID, query.Get("client_id"))
	require.Equal(t, RedirectURL, query.Get("redirect_uri"))
	require.Equal(t, Scope, query.Get("scope"))
	require.NotEmpty(t, query.Get("code_challenge"))
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.Equal(t, flow.State, query.Get("state"))
	require.Equal(t, "true", query.Get("id_token_add_organizations"))
	require.Equal(t, "true", query.Get("codex_cli_simplified_flow"))
	require.Equal(t, HeaderOriginatorVal, query.Get("originator"))
}

func TestExchangeAuthorizationCode(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, tokenURL, req.URL.String())
		require.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		values, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "authorization_code", values.Get("grant_type"))
		require.Equal(t, oauthClientID, values.Get("client_id"))
		require.Equal(t, "test-code", values.Get("code"))
		require.Equal(t, "test-verifier", values.Get("code_verifier"))
		require.Equal(t, RedirectURL, values.Get("redirect_uri"))

		return jsonResponse(http.StatusOK, `{
			"access_token":"test-access-token",
			"refresh_token":"test-refresh-token",
			"expires_in":3600
		}`), nil
	}))
	defer restore()

	token, err := ExchangeAuthorizationCode(context.Background(), "test-code", "test-verifier")
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, "test-access-token", token.AccessToken)
	require.Equal(t, "test-refresh-token", token.RefreshToken)
	require.Equal(t, 3600, token.ExpiresIn)
}

func TestExchangeAuthorizationCodeFailure(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"error":"invalid_grant"}`), nil
	}))
	defer restore()

	token, err := ExchangeAuthorizationCode(context.Background(), "test-code", "test-verifier")
	require.Nil(t, token)
	require.ErrorContains(t, err, "token request failed: 400 Bad Request")
}

func TestRefreshToken(t *testing.T) {
	restore := SetTokenRequesterForTest(testTokenRequester(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		values, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "refresh_token", values.Get("grant_type"))
		require.Equal(t, oauthClientID, values.Get("client_id"))
		require.Equal(t, "test-refresh-token", values.Get("refresh_token"))

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

func TestRefreshTokenRequiresRefreshToken(t *testing.T) {
	t.Parallel()

	token, err := RefreshToken(context.Background(), "")
	require.Nil(t, token)
	require.ErrorContains(t, err, "refresh token is required")
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
			input:     "http://localhost:1455/auth/callback?code=test-code&state=test-state",
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

func TestCallbackHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		query          string
		wantStatusCode int
		wantBody       string
		wantCode       string
	}{
		{
			name:           "success",
			query:          "?code=test-code&state=test-state",
			wantStatusCode: http.StatusOK,
			wantBody:       "Authorization complete",
			wantCode:       "test-code",
		},
		{
			name:           "state mismatch",
			query:          "?code=test-code&state=wrong-state",
			wantStatusCode: http.StatusBadRequest,
			wantBody:       "State mismatch",
		},
		{
			name:           "missing code",
			query:          "?state=test-state",
			wantStatusCode: http.StatusBadRequest,
			wantBody:       "Missing authorization code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codeCh := make(chan string, 1)
			handler := newCallbackHandler("test-state", codeCh)

			req := httptest.NewRequest(http.MethodGet, callbackPath+tt.query, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatusCode, rec.Code)
			require.Contains(t, rec.Body.String(), tt.wantBody)

			select {
			case code := <-codeCh:
				require.Equal(t, tt.wantCode, code)
			default:
				require.Empty(t, tt.wantCode)
			}
		})
	}
}

func TestExtractAccountID(t *testing.T) {
	t.Parallel()

	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"test-account-id"}}`
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"

	accountID, err := ExtractAccountID(token)
	require.NoError(t, err)
	require.Equal(t, "test-account-id", accountID)

	_, err = ExtractAccountID("invalid-jwt")
	require.Error(t, err)
}

func TestSetExpiresAt(t *testing.T) {
	t.Parallel()

	token := &oauth.Token{ExpiresIn: 3600}
	token.SetExpiresAt()
	require.WithinDuration(t, time.Now().Add(time.Hour), time.Unix(token.ExpiresAt, 0), time.Second)
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
