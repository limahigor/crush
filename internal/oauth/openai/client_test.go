package openai

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCodexTransportSetsExpectedHeaders(t *testing.T) {
	t.Parallel()

	var captured *http.Request
	transport := &codexTransport{
		base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			captured = req
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", http.NoBody)
	require.NoError(t, err)
	req.Header.Set(HeaderAccountID, "acct-123")
	req.Header.Set(HeaderOpenAIBeta, HeaderOpenAIBetaVal)
	req.Header.Set("User-Agent", "OpenAI/Go test")

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, captured)

	require.Equal(t, codexUserAgent(), captured.Header.Get("User-Agent"))
	require.Equal(t, []string{HeaderOriginatorVal}, captured.Header[HeaderOriginator])
	require.Equal(t, []string{HeaderOpenAIBetaVal}, captured.Header[HeaderOpenAIBeta])
	require.Equal(t, []string{"acct-123"}, captured.Header[headerAccountIDCanonical])
	require.Equal(t, chatGPTOrigin, captured.Header.Get(headerOrigin))
	require.Equal(t, chatGPTReferer, captured.Header.Get(headerReferer))
	require.Contains(t, captured.Header, headerAccountIDCanonical)
	require.NotContains(t, captured.Header, http.CanonicalHeaderKey(HeaderAccountID))
}
