package dialog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/browserutil"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	openaioauth "github.com/charmbracelet/crush/internal/oauth/openai"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
)

type openAICodexAuthInitMsg struct {
	flow      openaioauth.AuthFlow
	server    *openaioauth.LocalServer
	err       error
	afterInit openAIAuthInitAction
}

type openAICodexAuthTokenMsg struct {
	token *oauth.Token
}

type openAICodexAuthErrorMsg struct {
	err error
}

type openAICodexAuthManualMsg struct {
	err         error
	stopPolling bool
}

// OAuthOpenAI handles the OpenAI Codex OAuth flow authentication.
type OAuthOpenAI struct {
	com          *common.Common
	isOnboarding bool

	provider  catwalk.Provider
	model     config.SelectedModel
	modelType config.SelectedModelType

	State OAuthState

	spinner spinner.Model
	help    help.Model
	keyMap  struct {
		Copy   key.Binding
		Submit key.Binding
		Close  key.Binding
	}

	width int

	flow      openaioauth.AuthFlow
	server    *openaioauth.LocalServer
	token     *oauth.Token
	input     textinput.Model
	lastError error
	cancel    context.CancelFunc

	manualMode       bool
	manualSubmitting bool
	statusNote       string
}

type openAIAuthInitAction int

const (
	openAIAuthInitActionNone openAIAuthInitAction = iota
	openAIAuthInitActionOpenBrowser
	openAIAuthInitActionCopyURL
)

var _ Dialog = (*OAuthOpenAI)(nil)

// NewOAuthOpenAI creates a new OpenAI OAuth dialog.
func NewOAuthOpenAI(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuthOpenAI, tea.Cmd) {
	t := com.Styles

	m := OAuthOpenAI{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.provider = provider
	m.model = model
	m.modelType = modelType
	m.width = 60
	m.State = OAuthStateInitializing

	flow, err := openaioauth.CreateAuthorizationFlow()
	if err != nil {
		m.State = OAuthStateError
		m.lastError = err
	} else {
		m.flow = flow
		m.State = OAuthStateDisplay
	}

	innerWidth := m.width - t.Dialog.View.GetHorizontalFrameSize() - 2

	m.spinner = spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(t.Dialog.SecondaryText),
	)

	m.input = textinput.New()
	m.input.SetVirtualCursor(false)
	m.input.Placeholder = "Paste the redirect URL or authorization code"
	m.input.SetStyles(com.Styles.TextInput)
	m.input.SetWidth(max(0, innerWidth-t.Dialog.InputPrompt.GetHorizontalFrameSize()-1))

	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()

	m.keyMap.Copy = key.NewBinding(
		key.WithKeys("c", "C"),
		key.WithHelp("c", "copy url"),
	)
	m.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "open browser"),
	)
	m.keyMap.Close = CloseKey

	return &m, tea.Batch(m.spinner.Tick, m.beginAuth(openAIAuthInitActionOpenBrowser))
}

// ID implements Dialog.
func (m *OAuthOpenAI) ID() string {
	return OAuthID
}

// HandleMsg handles messages and state transitions.
func (m *OAuthOpenAI) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		switch m.State {
		case OAuthStateInitializing, OAuthStateDisplay:
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}

	case openAICodexAuthInitMsg:
		if msg.err != nil && msg.flow.URL == "" {
			m.State = OAuthStateError
			m.lastError = msg.err
			return nil
		}
		m.flow = msg.flow
		m.server = msg.server
		m.State = OAuthStateDisplay
		if msg.err != nil {
			m.enterManualMode(msg.err)
			return ActionCmd{m.postInitCmd(msg.afterInit, false)}
		}
		return ActionCmd{m.postInitCmd(msg.afterInit, true)}

	case openAICodexAuthTokenMsg:
		m.State = OAuthStateSuccess
		m.token = msg.token
		m.manualSubmitting = false
		m.stopPolling()
		return nil

	case openAICodexAuthManualMsg:
		if msg.stopPolling {
			m.stopPolling()
		}
		m.enterManualMode(msg.err)
		return nil

	case openAICodexAuthErrorMsg:
		m.State = OAuthStateError
		m.lastError = msg.err
		m.manualSubmitting = false
		m.stopPolling()
		return ActionCmd{util.ReportError(msg.err)}

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Copy):
			return ActionCmd{m.copyURL()}
		case key.Matches(msg, m.keyMap.Submit):
			switch m.State {
			case OAuthStateSuccess:
				return m.saveKeyAndContinue()
			default:
				if m.manualMode {
					if strings.TrimSpace(m.input.Value()) == "" {
						m.statusNote = "Enter pressed. Opening the browser again."
						return ActionCmd{m.openURL()}
					}
					m.statusNote = "Enter pressed. Submitting the pasted authorization data."
					m.manualSubmitting = true
					m.lastError = nil
					return ActionCmd{m.submitManualEntry(m.input.Value())}
				}
				m.statusNote = "Enter pressed. Preparing OpenAI authentication."
				return ActionCmd{m.openURL()}
			}
		case key.Matches(msg, m.keyMap.Close):
			switch m.State {
			case OAuthStateSuccess:
				return m.saveKeyAndContinue()
			default:
				m.stopPolling()
				return ActionClose{}
			}
		case m.manualMode && !m.manualSubmitting:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.PasteMsg:
		if m.manualMode && !m.manualSubmitting {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	}
	return nil
}

// Draw implements Dialog.
func (m *OAuthOpenAI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	dialogStyle := t.Dialog.View.Width(m.width)
	cur := m.Cursor()
	if m.isOnboarding {
		view := m.dialogContent()
		DrawOnboardingCursor(scr, area, view, cur)
		if cur != nil {
			cur.Y -= 1
			cur.X -= 1
		}
	} else {
		view := dialogStyle.Render(m.dialogContent())
		DrawCenterCursor(scr, area, view, cur)
	}
	return cur
}

func (m *OAuthOpenAI) dialogContent() string {
	t := m.com.Styles
	helpStyle := t.Dialog.HelpView

	switch m.State {
	case OAuthStateInitializing:
		return m.innerDialogContent()
	default:
		elements := []string{
			m.headerContent(),
			m.innerDialogContent(),
			helpStyle.Render(m.help.View(m)),
		}
		return strings.Join(elements, "\n")
	}
}

func (m *OAuthOpenAI) headerContent() string {
	t := m.com.Styles
	titleStyle := t.Dialog.Title
	textStyle := t.Dialog.PrimaryText
	dialogStyle := t.Dialog.View.Width(m.width)
	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	if m.isOnboarding {
		return textStyle.Render(m.dialogTitle())
	}
	return common.DialogTitle(t, titleStyle.Render(m.dialogTitle()), m.width-headerOffset, t.WorkingGradFromColor, t.WorkingGradToColor)
}

func (m *OAuthOpenAI) innerDialogContent() string {
	t := m.com.Styles
	textStyle := t.Dialog.PrimaryText
	muted := t.Dialog.SecondaryText

	switch m.State {
	case OAuthStateInitializing:
		return muted.Render(m.spinner.View() + "Starting OpenAI Codex authentication...")
	case OAuthStateDisplay:
		if m.manualMode {
			parts := []string{
				textStyle.Render("Complete authentication in the browser, then paste the full redirect URL or the authorization code below."),
				"",
				muted.Render("Open this URL if the browser did not open automatically:"),
				t.Dialog.SecondaryText.Hyperlink(m.flow.URL, "id=openai-codex-auth").Render(m.flow.URL),
				"",
			}
			if m.lastError != nil {
				parts = append(parts, t.Dialog.TitleError.Render(m.lastError.Error()), "")
			}
			parts = append(parts,
				t.Dialog.InputPrompt.Render(m.inputView()),
				muted.Render("Press enter with an empty field to open the browser again."),
			)
			if m.statusNote != "" {
				parts = append(parts, muted.Render(m.statusNote))
			}
			return strings.Join(parts, "\n")
		}
		link := t.Dialog.SecondaryText.Hyperlink(m.flow.URL, "id=openai-codex-auth").Render(m.flow.URL)
		parts := []string{
			textStyle.Render("Press enter to open the browser and complete authentication."),
			"",
			muted.Render("Browser not opening? Open this URL manually:"),
			link,
			"",
			muted.Render(m.spinner.View() + "Waiting for authorization..."),
		}
		if m.statusNote != "" {
			parts = append(parts, "", muted.Render(m.statusNote))
		}
		return strings.Join(parts, "\n")
	case OAuthStateSuccess:
		return textStyle.Render("Authentication successful! Press enter to continue.")
	case OAuthStateError:
		errMsg := "Authentication failed. Try \"crush login openai-codex\" from the CLI."
		if m.lastError != nil {
			errMsg = fmt.Sprintf("Authentication failed: %v", m.lastError)
		}
		return t.Dialog.TitleError.Render(errMsg)
	default:
		return ""
	}
}

func (m *OAuthOpenAI) dialogTitle() string {
	t := m.com.Styles
	textStyle := t.Dialog.TitleText
	accentStyle := t.Dialog.TitleAccent
	return textStyle.Render("Authenticate with ") + accentStyle.Render("OpenAI Codex") + textStyle.Render(".")
}

// FullHelp returns the full help view.
func (m *OAuthOpenAI) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keyMap.Copy, m.submitBinding(), m.keyMap.Close}}
}

// ShortHelp returns the short help view.
func (m *OAuthOpenAI) ShortHelp() []key.Binding {
	return []key.Binding{m.keyMap.Copy, m.submitBinding(), m.keyMap.Close}
}

func (m *OAuthOpenAI) beginAuth(afterInit openAIAuthInitAction) tea.Cmd {
	return func() tea.Msg {
		flow := m.flow
		if flow.URL == "" {
			var err error
			flow, err = openaioauth.CreateAuthorizationFlow()
			if err != nil {
				return openAICodexAuthInitMsg{err: err, afterInit: afterInit}
			}
		}
		server, err := openaioauth.StartLocalServer(flow.State)
		if err != nil {
			return openAICodexAuthInitMsg{flow: flow, err: err, afterInit: afterInit}
		}
		return openAICodexAuthInitMsg{flow: flow, server: server, afterInit: afterInit}
	}
}

func (m *OAuthOpenAI) startPolling() tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.cancel = cancel
	return func() tea.Msg {
		if m.server == nil {
			return openAICodexAuthManualMsg{
				err:         fmt.Errorf("OAuth callback server not available"),
				stopPolling: true,
			}
		}
		defer m.server.Close()
		code, err := m.server.WaitForCode(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return openAICodexAuthManualMsg{
					err:         fmt.Errorf("Timed out waiting for the OAuth callback"),
					stopPolling: true,
				}
			}
			return openAICodexAuthErrorMsg{err: err}
		}
		token, err := openaioauth.ExchangeAuthorizationCode(ctx, code, m.flow.Verifier)
		if err != nil {
			return openAICodexAuthErrorMsg{err: err}
		}
		return openAICodexAuthTokenMsg{token: token}
	}
}

func (m *OAuthOpenAI) stopPolling() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.server != nil {
		_ = m.server.Close()
		m.server = nil
	}
}

func (m *OAuthOpenAI) openURL() tea.Cmd {
	m.statusNote = "Enter pressed. Opening the browser."
	if m.flow.URL == "" {
		return m.beginAuth(openAIAuthInitActionOpenBrowser)
	}
	return func() tea.Msg {
		if err := browserutil.OpenURL(m.flow.URL); err != nil {
			return openAICodexAuthManualMsg{err: fmt.Errorf("failed to open browser: %w", err)}
		}
		return nil
	}
}

func (m *OAuthOpenAI) copyURL() tea.Cmd {
	m.statusNote = "C pressed. Copying the authorization URL."
	if m.flow.URL == "" {
		return m.beginAuth(openAIAuthInitActionCopyURL)
	}
	return tea.Sequence(
		tea.SetClipboard(m.flow.URL),
		util.ReportInfo("URL copied to clipboard"),
	)
}

func (m *OAuthOpenAI) saveKeyAndContinue() Action {
	err := m.com.Workspace.SetProviderAPIKey(config.ScopeGlobal, string(m.provider.ID), m.token)
	if err != nil {
		return ActionCmd{util.ReportError(fmt.Errorf("failed to save API key: %w", err))}
	}

	return ActionSelectModel{
		Provider:  m.provider,
		Model:     m.model,
		ModelType: m.modelType,
	}
}

// Cursor returns the cursor position relative to the dialog.
func (m *OAuthOpenAI) Cursor() *tea.Cursor {
	if !m.manualMode {
		return nil
	}
	cur := InputCursor(m.com.Styles, m.input.Cursor())
	if cur == nil {
		return nil
	}
	cur.Y += m.manualInputOffset()
	return cur
}

func (m *OAuthOpenAI) inputView() string {
	if m.manualSubmitting {
		m.input.Blur()
		m.input.Prompt = m.spinner.View()
		return m.input.View()
	}
	m.input.Focus()
	m.input.Prompt = "> "
	return m.input.View()
}

func (m *OAuthOpenAI) submitBinding() key.Binding {
	switch {
	case m.State == OAuthStateSuccess:
		return key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "continue"),
		)
	case m.manualMode:
		return key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "submit"),
		)
	default:
		return m.keyMap.Submit
	}
}

func (m *OAuthOpenAI) enterManualMode(err error) {
	m.manualMode = true
	m.manualSubmitting = false
	m.lastError = err
	m.State = OAuthStateDisplay
	m.input.Focus()
}

func (m *OAuthOpenAI) postInitCmd(afterInit openAIAuthInitAction, startPolling bool) tea.Cmd {
	var cmds []tea.Cmd
	if startPolling {
		cmds = append(cmds, m.startPolling())
	}
	switch afterInit {
	case openAIAuthInitActionCopyURL:
		cmds = append(cmds, m.copyURL())
	case openAIAuthInitActionOpenBrowser:
		cmds = append(cmds, m.openURL())
	}
	return tea.Batch(cmds...)
}

func (m *OAuthOpenAI) submitManualEntry(value string) tea.Cmd {
	return func() tea.Msg {
		value = strings.TrimSpace(value)
		if value == "" {
			return m.openURL()()
		}

		code, state := openaioauth.ParseAuthorizationInput(value)
		if code == "" {
			return openAICodexAuthManualMsg{err: fmt.Errorf("missing authorization code in pasted value")}
		}
		if state != "" && state != m.flow.State {
			return openAICodexAuthManualMsg{err: fmt.Errorf("state mismatch in pasted redirect URL")}
		}

		token, err := openaioauth.ExchangeAuthorizationCode(context.Background(), code, m.flow.Verifier)
		if err != nil {
			return openAICodexAuthManualMsg{err: err}
		}
		return openAICodexAuthTokenMsg{token: token}
	}
}

func (m *OAuthOpenAI) manualInputOffset() int {
	prefixLines := 5
	if m.lastError != nil {
		prefixLines += 2
	}
	return prefixLines
}
