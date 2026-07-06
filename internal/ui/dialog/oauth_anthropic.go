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
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/browserutil"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	anthropicoauth "github.com/charmbracelet/crush/internal/oauth/anthropic"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/exp/charmtone"
)

type anthropicAuthInitMsg struct {
	flow      anthropicoauth.AuthFlow
	err       error
	afterInit anthropicAuthInitAction
}

type anthropicAuthTokenMsg struct {
	token *oauth.Token
}

type anthropicAuthErrorMsg struct {
	err error
}

type anthropicAuthManualMsg struct {
	err error
}

// OAuthAnthropic handles the Anthropic OAuth flow authentication.
type OAuthAnthropic struct {
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

	flow      anthropicoauth.AuthFlow
	token     *oauth.Token
	input     textinput.Model
	lastError error

	manualSubmitting bool
	statusNote       string
}

type anthropicAuthInitAction int

const (
	anthropicAuthInitActionNone anthropicAuthInitAction = iota
	anthropicAuthInitActionOpenBrowser
	anthropicAuthInitActionCopyURL
)

var _ Dialog = (*OAuthAnthropic)(nil)

// NewOAuthAnthropic creates a new Anthropic OAuth dialog.
func NewOAuthAnthropic(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuthAnthropic, tea.Cmd) {
	t := com.Styles

	m := OAuthAnthropic{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.provider = provider
	m.model = model
	m.modelType = modelType
	m.width = 60
	m.State = OAuthStateInitializing

	flow, err := anthropicoauth.CreateAuthorizationFlow()
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
		spinner.WithStyle(t.Dialog.PrimaryText.Foreground(charmtone.Julep)),
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

	return &m, tea.Batch(m.spinner.Tick, m.beginAuth(anthropicAuthInitActionOpenBrowser))
}

// ID implements Dialog.
func (m *OAuthAnthropic) ID() string {
	return OAuthID
}

// HandleMsg handles messages and state transitions.
func (m *OAuthAnthropic) HandleMsg(msg tea.Msg) Action {
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

	case anthropicAuthInitMsg:
		if msg.err != nil && msg.flow.URL == "" {
			m.State = OAuthStateError
			m.lastError = msg.err
			return nil
		}
		m.flow = msg.flow
		m.State = OAuthStateDisplay
		if msg.err != nil {
			m.enterManualMode(msg.err)
			return ActionCmd{m.postInitCmd(msg.afterInit)}
		}
		return ActionCmd{m.postInitCmd(msg.afterInit)}

	case anthropicAuthTokenMsg:
		m.State = OAuthStateSuccess
		m.token = msg.token
		m.manualSubmitting = false
		return nil

	case anthropicAuthManualMsg:
		m.enterManualMode(msg.err)
		return nil

	case anthropicAuthErrorMsg:
		m.State = OAuthStateError
		m.lastError = msg.err
		m.manualSubmitting = false
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
				if strings.TrimSpace(m.input.Value()) == "" {
					m.statusNote = "Enter pressed. Opening the browser again."
					return ActionCmd{m.openURL()}
				}
				m.statusNote = "Enter pressed. Submitting the pasted authorization data."
				m.manualSubmitting = true
				m.lastError = nil
				return ActionCmd{m.submitManualEntry(m.input.Value())}
			}
		case key.Matches(msg, m.keyMap.Close):
			switch m.State {
			case OAuthStateSuccess:
				return m.saveKeyAndContinue()
			default:
				return ActionClose{}
			}
		case !m.manualSubmitting:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.PasteMsg:
		if !m.manualSubmitting {
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
func (m *OAuthAnthropic) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
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

func (m *OAuthAnthropic) dialogContent() string {
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

func (m *OAuthAnthropic) headerContent() string {
	t := m.com.Styles
	titleStyle := t.Dialog.Title
	textStyle := t.Dialog.PrimaryText
	dialogStyle := t.Dialog.View.Width(m.width)
	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	if m.isOnboarding {
		return textStyle.Render(m.dialogTitle())
	}
	return common.DialogTitle(t, titleStyle.Render(m.dialogTitle()), m.width-headerOffset, charmtone.Charple, charmtone.Dolly)
}

func (m *OAuthAnthropic) innerDialogContent() string {
	content, _ := m.buildDisplayContent()
	return content
}

// buildDisplayContent renders the display state and returns both the joined
// content and the vertical offset (in terminal rows) of the input row relative
// to the top of innerDialogContent. Returning both together ensures the
// cursor offset always tracks the actual rendered layout — including wrapped
// paragraphs and multi-line hyperlinks.
func (m *OAuthAnthropic) buildDisplayContent() (string, int) {
	t := m.com.Styles
	textStyle := t.Dialog.PrimaryText
	muted := t.Dialog.SecondaryText

	switch m.State {
	case OAuthStateInitializing:
		return muted.Render(m.spinner.View() + "Starting Anthropic authentication..."), 0
	case OAuthStateDisplay:
		wrapWidth := m.contentWrapWidth()
		wrap := func(style lipgloss.Style) lipgloss.Style {
			if wrapWidth > 0 {
				return style.Width(wrapWidth)
			}
			return style
		}
		before := []string{
			wrap(textStyle).Render("Complete authentication in the browser, then paste the full redirect URL or the authorization code below."),
			"",
			wrap(muted).Render("Open this URL if the browser did not open automatically:"),
			wrap(t.Dialog.SecondaryText).Hyperlink(m.flow.URL, "id=anthropic-auth").Render(m.flow.URL),
			"",
		}
		if m.lastError != nil {
			before = append(before, wrap(t.Dialog.TitleError).Render(m.lastError.Error()), "")
		}
		after := []string{
			t.Dialog.InputPrompt.Render(m.inputView()),
			wrap(muted).Render("Press enter with an empty field to open the browser again."),
		}
		if m.statusNote != "" {
			after = append(after, wrap(muted).Render(m.statusNote))
		}
		joined := strings.Join(append(before, after...), "\n")
		beforeJoined := strings.Join(before, "\n")
		offset := 0
		if beforeJoined != "" {
			offset = lipgloss.Height(beforeJoined)
		}
		return joined, offset
	case OAuthStateSuccess:
		return textStyle.Render("Authentication successful! Press enter to continue."), 0
	case OAuthStateError:
		errMsg := "Authentication failed. Try \"crush login anthropic\" from the CLI."
		if m.lastError != nil {
			errMsg = fmt.Sprintf("Authentication failed: %v", m.lastError)
		}
		return t.Dialog.TitleError.Render(errMsg), 0
	default:
		return "", 0
	}
}

// contentWrapWidth returns the horizontal budget (columns) available for text
// content inside the dialog, taking the dialog frame into account. Both onboarding
// and centered variants render at the same effective content width.
func (m *OAuthAnthropic) contentWrapWidth() int {
	frame := m.com.Styles.Dialog.View.GetHorizontalFrameSize()
	return max(0, m.width-frame)
}

func (m *OAuthAnthropic) dialogTitle() string {
	t := m.com.Styles
	textStyle := t.Dialog.TitleText
	accentStyle := t.Dialog.TitleAccent
	return textStyle.Render("Authenticate with ") + accentStyle.Render("Anthropic") + textStyle.Render(".")
}

// FullHelp returns the full help view.
func (m *OAuthAnthropic) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keyMap.Copy, m.submitBinding(), m.keyMap.Close}}
}

// ShortHelp returns the short help view.
func (m *OAuthAnthropic) ShortHelp() []key.Binding {
	return []key.Binding{m.keyMap.Copy, m.submitBinding(), m.keyMap.Close}
}

func (m *OAuthAnthropic) beginAuth(afterInit anthropicAuthInitAction) tea.Cmd {
	return func() tea.Msg {
		flow := m.flow
		if flow.URL == "" {
			var err error
			flow, err = anthropicoauth.CreateAuthorizationFlow()
			if err != nil {
				return anthropicAuthInitMsg{err: err, afterInit: afterInit}
			}
		}
		return anthropicAuthInitMsg{flow: flow, afterInit: afterInit}
	}
}

func (m *OAuthAnthropic) openURL() tea.Cmd {
	return func() tea.Msg {
		if m.flow.URL == "" {
			return anthropicAuthInitMsg{afterInit: anthropicAuthInitActionOpenBrowser}
		}
		if err := browserutil.OpenURL(m.flow.URL); err != nil {
			return anthropicAuthManualMsg{err: fmt.Errorf("failed to open browser: %w", err)}
		}
		return nil
	}
}

func (m *OAuthAnthropic) copyURL() tea.Cmd {
	return func() tea.Msg {
		if m.flow.URL == "" {
			return anthropicAuthInitMsg{afterInit: anthropicAuthInitActionCopyURL}
		}
		return common.CopyToClipboard(m.flow.URL, "URL copied to clipboard")()
	}
}

func (m *OAuthAnthropic) saveKeyAndContinue() Action {
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

func (m *OAuthAnthropic) Cursor() *tea.Cursor {
	if m.State != OAuthStateDisplay || m.manualSubmitting {
		return nil
	}
	cur := InputCursor(m.com.Styles, m.input.Cursor())
	if cur == nil {
		return nil
	}
	cur.Y += m.manualInputOffset()
	return cur
}

func (m *OAuthAnthropic) inputView() string {
	if m.manualSubmitting {
		m.input.Blur()
		m.input.Prompt = m.spinner.View()
		return m.input.View()
	}
	m.input.Focus()
	m.input.Prompt = "> "
	return m.input.View()
}

func (m *OAuthAnthropic) submitBinding() key.Binding {
	if m.State == OAuthStateSuccess {
		return key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "continue"),
		)
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		return key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "open browser"),
		)
	}
	return key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "submit"),
	)
}

func (m *OAuthAnthropic) enterManualMode(err error) {
	m.State = OAuthStateDisplay
	m.lastError = err
	m.manualSubmitting = false
	m.input.Focus()
}

func (m *OAuthAnthropic) postInitCmd(afterInit anthropicAuthInitAction) tea.Cmd {
	switch afterInit {
	case anthropicAuthInitActionCopyURL:
		return m.copyURL()
	case anthropicAuthInitActionOpenBrowser:
		return m.openURL()
	default:
		return nil
	}
}

func (m *OAuthAnthropic) submitManualEntry(value string) tea.Cmd {
	return func() tea.Msg {
		code, state := anthropicoauth.ParseAuthorizationInput(value)
		if code == "" {
			return anthropicAuthManualMsg{err: fmt.Errorf("authorization code not found in the pasted value")}
		}
		if state != "" && state != m.flow.State {
			return anthropicAuthManualMsg{err: fmt.Errorf("state mismatch in pasted callback URL")}
		}
		if state == "" {
			state = m.flow.State
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		token, err := anthropicoauth.ExchangeAuthorizationCode(ctx, code, state, m.flow.Verifier)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return anthropicAuthErrorMsg{err: err}
		}
		return anthropicAuthTokenMsg{token: token}
	}
}

func (m *OAuthAnthropic) manualInputOffset() int {
	_, offset := m.buildDisplayContent()
	return offset
}
