package cmd

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/crush/internal/browserutil"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"github.com/charmbracelet/crush/internal/oauth/hyper"
	openaioauth "github.com/charmbracelet/crush/internal/oauth/openai"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Aliases: []string{"auth"},
	Use:     "login [platform]",
	Short:   "Login Crush to a platform",
	Long: `Login Crush to a specified platform.
The platform should be provided as an argument.
Available platforms are: hyper, copilot.`,
	Example: `
# Authenticate with Charm Hyper
crush login

# Authenticate with GitHub Copilot
crush login copilot

# Force re-authentication even if already logged in
crush login -f copilot

# Authenticate with OpenAI Codex (ChatGPT OAuth)
crush login openai-codex
  `,
	ValidArgs: []cobra.Completion{
		"hyper",
		"copilot",
		"openai",
		"openai-codex",
		"codex",
		"chatgpt",
		"github",
		"github-copilot",
	},
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, ws, cleanup, err := connectToServer(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		progressEnabled := ws.Config.Options.Progress == nil || *ws.Config.Options.Progress
		if progressEnabled && supportsProgressBar() {
			_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
			defer func() { _, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar) }()
		}

		provider := "hyper"
		if len(args) > 0 {
			provider = args[0]
		}
		force, _ := cmd.Flags().GetBool("force")
		switch provider {
		case "hyper":
			return loginHyper(c, ws.ID, force)
		case "copilot", "github", "github-copilot":
			return loginCopilot(c, ws.ID, force)
		case "openai", "openai-codex", "codex", "chatgpt":
			return loginOpenAICodex(cmd.Context(), c, ws.ID)
		default:
			return fmt.Errorf("unknown platform: %s", args[0])
		}
	},
}

func init() {
	loginCmd.Flags().BoolP("force", "f", false, "Force re-authentication even if already logged in")
}

func loginHyper(c *client.Client, wsID string, force bool) error {
	ctx := getLoginContext()

	if !force {
		cfg, err := c.GetConfig(ctx, wsID)
		if err == nil && cfg != nil {
			if pc, ok := cfg.Providers.Get("hyper"); ok && pc.OAuthToken != nil {
				fmt.Println("You are already logged in to Hyper.")
				fmt.Println("Use --force to re-authenticate.")
				return nil
			}
		}
	}

	resp, err := hyper.InitiateDeviceAuth(ctx)
	if err != nil {
		return err
	}

	if clipboard.WriteAll(resp.UserCode) == nil {
		fmt.Println("The following code should be on clipboard already:")
	} else {
		fmt.Println("Copy the following code:")
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Render(resp.UserCode))
	fmt.Println()
	fmt.Println("Press enter to open this URL, and then paste it there:")
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Hyperlink(resp.VerificationURL, "id=hyper").Render(resp.VerificationURL))
	fmt.Println()
	waitEnter()
	if err := browser.OpenURL(resp.VerificationURL); err != nil {
		fmt.Println("Could not open the URL. You'll need to manually open the URL in your browser.")
	}

	fmt.Println("Exchanging authorization code...")
	refreshToken, err := hyper.PollForToken(ctx, resp.DeviceCode, resp.ExpiresIn)
	if err != nil {
		return err
	}

	fmt.Println("Exchanging refresh token for access token...")
	token, err := hyper.ExchangeToken(ctx, refreshToken)
	if err != nil {
		return err
	}

	fmt.Println("Verifying access token...")
	introspect, err := hyper.IntrospectToken(ctx, token.AccessToken)
	if err != nil {
		return fmt.Errorf("token introspection failed: %w", err)
	}
	if !introspect.Active {
		return fmt.Errorf("access token is not active")
	}

	if err := cmp.Or(
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.hyper.api_key", token.AccessToken),
		c.SetConfigField(ctx, wsID, config.ScopeGlobal, "providers.hyper.oauth", token),
	); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with Hyper!")
	return nil
}

func loginCopilot(c *client.Client, wsID string, force bool) error {
	loginCtx := getLoginContext()

	if !force {
		cfg, err := c.GetConfig(loginCtx, wsID)
		if err == nil && cfg != nil {
			if pc, ok := cfg.Providers.Get("copilot"); ok && pc.OAuthToken != nil {
				fmt.Println("You are already logged in to GitHub Copilot.")
				fmt.Println("Use --force to re-authenticate.")
				return nil
			}
		}
	}

	diskToken, hasDiskToken := copilot.RefreshTokenFromDisk()
	var token *oauth.Token

	switch {
	case hasDiskToken:
		fmt.Println("Found existing GitHub Copilot token on disk. Using it to authenticate...")

		t, err := copilot.RefreshToken(loginCtx, diskToken)
		if err != nil {
			return fmt.Errorf("unable to refresh token from disk: %w", err)
		}
		token = t
	default:
		fmt.Println("Requesting device code from GitHub...")
		dc, err := copilot.RequestDeviceCode(loginCtx)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Println("Open the following URL and follow the instructions to authenticate with GitHub Copilot:")
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Hyperlink(dc.VerificationURI, "id=copilot").Render(dc.VerificationURI))
		fmt.Println()
		fmt.Println("Code:", lipgloss.NewStyle().Bold(true).Render(dc.UserCode))
		fmt.Println()
		fmt.Println("Waiting for authorization...")

		t, err := copilot.PollForToken(loginCtx, dc)
		if err == copilot.ErrNotAvailable {
			fmt.Println()
			fmt.Println("GitHub Copilot is unavailable for this account. To signup, go to the following page:")
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Hyperlink(copilot.SignupURL, "id=copilot-signup").Render(copilot.SignupURL))
			fmt.Println()
			fmt.Println("You may be able to request free access if eligible. For more information, see:")
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Hyperlink(copilot.FreeURL, "id=copilot-free").Render(copilot.FreeURL))
		}
		if err != nil {
			return err
		}
		token = t
	}

	if err := cmp.Or(
		c.SetConfigField(loginCtx, wsID, config.ScopeGlobal, "providers.copilot.api_key", token.AccessToken),
		c.SetConfigField(loginCtx, wsID, config.ScopeGlobal, "providers.copilot.oauth", token),
	); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with GitHub Copilot!")
	return nil
}

func loginOpenAICodex(ctx context.Context, c *client.Client, wsID string) error {
	loginCtx := getLoginContext()

	cfg, err := c.GetConfig(ctx, wsID)
	if err == nil && cfg != nil {
		if pc, ok := cfg.Providers.Get(config.OpenAICodexProviderID); ok && pc.OAuthToken != nil {
			fmt.Println("You are already logged in to OpenAI Codex.")
			return nil
		}
	}

	flow, err := openaioauth.CreateAuthorizationFlow()
	if err != nil {
		return err
	}

	server, err := openaioauth.StartLocalServer(flow.State)
	if err != nil {
		fmt.Println("Could not start local callback server; falling back to manual login.")
		return completeOpenAICodexLogin(loginCtx, c, wsID, flow, true)
	}
	defer server.Close()

	fmt.Println("Opening browser for OpenAI Codex authentication...")
	if err := browserutil.OpenURL(flow.URL); err != nil {
		fmt.Println("Could not open the browser. Use this URL to continue:")
		fmt.Println(flow.URL)
	}

	code, err := waitForOpenAICodexCode(loginCtx, server)
	if err != nil {
		fmt.Println("Timed out waiting for the OAuth callback; falling back to manual login.")
		return completeOpenAICodexLogin(loginCtx, c, wsID, flow, true)
	}

	token, err := openaioauth.ExchangeAuthorizationCode(loginCtx, code, flow.Verifier)
	if err != nil {
		return err
	}
	if err := c.SetProviderAPIKey(loginCtx, wsID, config.ScopeGlobal, config.OpenAICodexProviderID, token); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with OpenAI Codex!")
	return nil
}

func waitForOpenAICodexCode(ctx context.Context, server *openaioauth.LocalServer) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return server.WaitForCode(ctx)
}

func completeOpenAICodexLogin(ctx context.Context, c *client.Client, wsID string, flow openaioauth.AuthFlow, printURL bool) error {
	token, err := loginOpenAICodexManual(ctx, flow, printURL)
	if err != nil {
		return err
	}
	if err := c.SetProviderAPIKey(ctx, wsID, config.ScopeGlobal, config.OpenAICodexProviderID, token); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("You're now authenticated with OpenAI Codex!")
	return nil
}

func loginOpenAICodexManual(ctx context.Context, flow openaioauth.AuthFlow, printURL bool) (*oauth.Token, error) {
	if printURL {
		fmt.Println("Open the following URL in your browser and complete the login:")
		fmt.Println()
		fmt.Println(flow.URL)
		fmt.Println()
	}
	fmt.Println("Paste the full redirect URL (or just the code) and press enter:")
	codeInput, err := readLine()
	if err != nil {
		return nil, err
	}
	code, state := openaioauth.ParseAuthorizationInput(codeInput)
	if code == "" {
		return nil, fmt.Errorf("authorization code not provided")
	}
	if state != "" && state != flow.State {
		return nil, fmt.Errorf("authorization state does not match, please retry")
	}
	return openaioauth.ExchangeAuthorizationCode(ctx, code, flow.Verifier)
}

func readLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func getLoginContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	go func() {
		<-ctx.Done()
		cancel()
		os.Exit(1)
	}()
	return ctx
}

func waitEnter() {
	_, _ = fmt.Scanln()
}
