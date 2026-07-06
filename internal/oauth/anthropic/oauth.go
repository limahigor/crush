package anthropic

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/oauth"
)

const (
	oauthClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	authorizeURL    = "https://claude.com/cai/oauth/authorize"
	defaultTokenURL = "https://platform.claude.com/v1/oauth/token"
)

var tokenURL = defaultTokenURL

type tokenRequester interface {
	Do(*http.Request) (*http.Response, error)
}

var tokenHTTPClient tokenRequester = &http.Client{Timeout: 20 * time.Second}

// SetTokenRequesterForTest overrides the HTTP client used for token requests.
func SetTokenRequesterForTest(client interface {
	Do(*http.Request) (*http.Response, error)
}) func() {
	previous := tokenHTTPClient
	tokenHTTPClient = client
	return func() {
		tokenHTTPClient = previous
	}
}

const (
	Scope       = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	RedirectURL = "https://platform.claude.com/oauth/code/callback"
	HeaderBeta  = "anthropic-beta"
	BetaOAuth   = "oauth-2025-04-20"
	UserAgent   = "claude-cli/2.1.87 (external, cli)"
	// tokenUserAgent mimics the axios HTTP client used by the reference
	// implementations; the OAuth token endpoint rejects generic user agents
	// with 429 rate-limit errors even on the first attempt.
	tokenUserAgent = "axios/1.13.6"
	tokenAccept    = "application/json, text/plain, */*"
)

// ClaudeCodeSystemPrompt must match the prefix accepted by Anthropic when
// authenticating with Claude Code subscriber tokens.
const ClaudeCodeSystemPrompt = "You are a Claude agent, built on Anthropic's Claude Agent SDK."

// AuthFlow holds the state for the OAuth2 authorization flow.
type AuthFlow struct {
	URL      string
	State    string
	Verifier string
}

// CreateAuthorizationFlow creates a new OAuth2 authorization flow.
func CreateAuthorizationFlow() (AuthFlow, error) {
	verifier, err := newCodeVerifier()
	if err != nil {
		return AuthFlow{}, err
	}

	state, err := newState()
	if err != nil {
		return AuthFlow{}, err
	}

	challenge := codeChallenge(verifier)

	// Preserve the exact parameter order Claude Code CLI uses. Go's
	// url.Values.Encode sorts keys alphabetically; some OAuth handlers along
	// this flow are order-sensitive, so we build the query manually.
	params := []struct{ key, value string }{
		{"code", "true"},
		{"client_id", oauthClientID},
		{"response_type", "code"},
		{"redirect_uri", RedirectURL},
		{"scope", Scope},
		{"code_challenge", challenge},
		{"code_challenge_method", "S256"},
		{"state", state},
	}
	var sb strings.Builder
	sb.WriteString(authorizeURL)
	sb.WriteByte('?')
	for i, p := range params {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(p.key)
		sb.WriteByte('=')
		sb.WriteString(url.QueryEscape(p.value))
	}

	return AuthFlow{
		URL:      sb.String(),
		State:    state,
		Verifier: verifier,
	}, nil
}

// ParseAuthorizationInput parses a pasted redirect URL or raw authorization
// code.
func ParseAuthorizationInput(input string) (code, state string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}

	if parsed, err := url.Parse(value); err == nil {
		code := parsed.Query().Get("code")
		state := parsed.Query().Get("state")
		if code != "" || state != "" {
			return code, state
		}
	}

	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}

	if strings.Contains(value, "code=") {
		params, err := url.ParseQuery(value)
		if err == nil {
			return params.Get("code"), params.Get("state")
		}
	}

	return value, ""
}

// authCodePayload matches the exact JSON field order expected by the token
// endpoint. Go's map[string]string marshaling sorts keys alphabetically,
// which reference clients don't; some upstream layers along this endpoint
// validate the payload shape strictly.
type authCodePayload struct {
	Code         string `json:"code"`
	State        string `json:"state"`
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
}

type refreshPayload struct {
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
}

// ExchangeAuthorizationCode exchanges an authorization code for an OAuth token.
// state should be the state value received in the callback (or the state
// generated during the authorization flow when the pasted input omits it).
func ExchangeAuthorizationCode(ctx context.Context, code, state, verifier string) (*oauth.Token, error) {
	if code == "" {
		return nil, errors.New("authorization code is required")
	}
	pureCode, parsedState := ParseAuthorizationInput(code)
	if parsedState != "" {
		state = parsedState
	}
	if state == "" {
		return nil, errors.New("authorization state is required")
	}
	payload := authCodePayload{
		Code:         pureCode,
		State:        state,
		GrantType:    "authorization_code",
		ClientID:     oauthClientID,
		RedirectURI:  RedirectURL,
		CodeVerifier: verifier,
	}
	return requestToken(ctx, payload)
}

// RefreshToken refreshes an Anthropic OAuth token.
func RefreshToken(ctx context.Context, refreshToken string) (*oauth.Token, error) {
	if refreshToken == "" {
		return nil, errors.New("refresh token is required")
	}
	payload := refreshPayload{
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
		ClientID:     oauthClientID,
	}
	return requestToken(ctx, payload)
}

func requestToken(ctx context.Context, payload any) (*oauth.Token, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode token request: %w", err)
	}

	const maxAttempts = 3
	var (
		resp         *http.Response
		responseBody []byte
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", tokenAccept)
		req.Header.Set("User-Agent", tokenUserAgent)

		resp, err = tokenHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to request token: %w", err)
		}
		responseBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read token response: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
			}
			continue
		}
		break
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed: %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var token oauth.Token
	if err := json.Unmarshal(responseBody, &token); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn == 0 {
		return nil, errors.New("token response missing required fields")
	}
	token.SetExpiresAt()
	return &token, nil
}

func newCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func newState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
