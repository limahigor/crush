package config

import "charm.land/catwalk/pkg/catwalk"

const (
	OpenAICodexProviderID   = "openai-codex"
	openAICodexBaseURL      = "https://chatgpt.com/backend-api/codex"
	openAICodexAPIKeyEnv    = "$OPENAI_CODEX_ACCESS_TOKEN"
	openAICodexInstructions = "You are a helpful coding assistant."
)

// OpenAICodexProvider returns the catwalk provider for the OpenAI Codex
// service.
func OpenAICodexProvider() catwalk.Provider {
	models := []catwalk.Model{
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.5",
			Name:             "GPT-5.5",
			ContextWindow:    1050000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.4",
			Name:             "GPT-5.4",
			ContextWindow:    1050000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.4-mini",
			Name:             "GPT-5.4 Mini",
			ContextWindow:    400000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.4-nano",
			Name:             "GPT-5.4 Nano",
			ContextWindow:    400000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.3-codex",
			Name:             "GPT-5.3 Codex",
			ContextWindow:    400000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.2",
			Name:             "GPT-5.2",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.2-codex",
			Name:             "GPT-5.2 Codex",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.1-codex-max",
			Name:             "GPT-5.1 Codex Max",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"low", "medium", "high", "xhigh"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.1-codex",
			Name:             "GPT-5.1 Codex",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"low", "medium", "high"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.1-codex-mini",
			Name:             "GPT-5.1 Codex Mini",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"medium", "high"},
			DefaultEffort:    "medium",
		}),
		openAICodexModel(openAICodexModelSpec{
			ID:               "gpt-5.1",
			Name:             "GPT-5.1",
			ContextWindow:    272000,
			DefaultMaxTokens: 128000,
			ReasoningLevels:  []string{"none", "low", "medium", "high"},
			DefaultEffort:    "medium",
		}),
	}

	return catwalk.Provider{
		ID:                  catwalk.InferenceProvider(OpenAICodexProviderID),
		Name:                "OpenAI Codex (ChatGPT OAuth)",
		APIEndpoint:         openAICodexBaseURL,
		APIKey:              openAICodexAPIKeyEnv,
		Type:                catwalk.TypeOpenAICompat,
		Models:              models,
		DefaultLargeModelID: "gpt-5.2-codex",
		DefaultSmallModelID: "gpt-5.1-codex-mini",
	}
}

// IsOpenAICodexProvider reports whether the provider is the ChatGPT OAuth
// backed Codex provider.
func IsOpenAICodexProvider(providerID string) bool {
	return providerID == OpenAICodexProviderID
}

type openAICodexModelSpec struct {
	ID               string
	Name             string
	ContextWindow    int64
	DefaultMaxTokens int64
	ReasoningLevels  []string
	DefaultEffort    string
}

func openAICodexModel(spec openAICodexModelSpec) catwalk.Model {
	return catwalk.Model{
		ID:                     spec.ID,
		Name:                   spec.Name,
		ContextWindow:          spec.ContextWindow,
		DefaultMaxTokens:       spec.DefaultMaxTokens,
		CanReason:              len(spec.ReasoningLevels) > 0,
		ReasoningLevels:        spec.ReasoningLevels,
		DefaultReasoningEffort: spec.DefaultEffort,
		SupportsImages:         true,
	}
}
