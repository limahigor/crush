package openai

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/version"
)

const (
	headerAccountIDCanonical = "ChatGPT-Account-ID"
	headerOrigin             = "Origin"
	headerReferer            = "Referer"
	chatGPTOrigin            = "https://chatgpt.com"
	chatGPTReferer           = chatGPTOrigin + "/"
)

// NewClient creates an HTTP client that matches the transport expectations of
// the ChatGPT-backed Codex endpoint.
func NewClient(debug bool) *http.Client {
	baseTransport := http.RoundTripper(http.DefaultTransport)
	if debug {
		baseTransport = log.NewHTTPClient().Transport
	}

	return &http.Client{
		Transport: &codexTransport{base: baseTransport},
	}
}

type codexTransport struct {
	base http.RoundTripper
}

func (t *codexTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("HTTP request is nil")
	}

	cloned := req.Clone(req.Context())
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}

	setExactHeader(cloned.Header, HeaderOriginator, HeaderOriginatorVal)
	setExactHeader(cloned.Header, "User-Agent", codexUserAgent())

	if beta := headerValue(cloned.Header, HeaderOpenAIBeta); beta != "" {
		setExactHeader(cloned.Header, HeaderOpenAIBeta, beta)
	}

	if accountID := headerValue(cloned.Header, HeaderAccountID); accountID != "" {
		setExactHeader(cloned.Header, headerAccountIDCanonical, accountID)
	}

	if strings.EqualFold(cloned.URL.Hostname(), "chatgpt.com") {
		if headerValue(cloned.Header, headerOrigin) == "" {
			setExactHeader(cloned.Header, headerOrigin, chatGPTOrigin)
		}
		if headerValue(cloned.Header, headerReferer) == "" {
			setExactHeader(cloned.Header, headerReferer, chatGPTReferer)
		}
	}

	return t.base.RoundTrip(cloned)
}

func headerValue(headers http.Header, key string) string {
	for existingKey, values := range headers {
		if !strings.EqualFold(existingKey, key) || len(values) == 0 {
			continue
		}
		return values[0]
	}
	return ""
}

func setExactHeader(headers http.Header, key, value string) {
	for existingKey := range headers {
		if strings.EqualFold(existingKey, key) {
			delete(headers, existingKey)
		}
	}
	headers[key] = []string{value}
}

func codexUserAgent() string {
	buildVersion := version.Version
	if buildVersion == "" || buildVersion == "devel" {
		buildVersion = "0.0.0"
	}

	return fmt.Sprintf(
		"%s/%s (%s; %s) crush",
		HeaderOriginatorVal,
		buildVersion,
		runtime.GOOS,
		codexGOARCH(runtime.GOARCH),
	)
}

func codexGOARCH(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	default:
		return goarch
	}
}
