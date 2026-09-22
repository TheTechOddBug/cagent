package acp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/teamloader"
	loaderdefaults "github.com/docker/docker-agent/pkg/teamloader/defaults"
	"github.com/docker/docker-agent/pkg/version"
)

// Agent implements the ACP Agent interface for docker agent.
type Agent struct {
	agentSource  config.Source
	runConfig    *config.RuntimeConfig
	sessionStore session.Store
	sessions     map[string]*Session

	conn     *acp.AgentSideConnection
	clientFS acp.FileSystemCapabilities
	team     *team.Team // Initialization validation only; sessions load their own teams.
	loadTeam func(context.Context, string) (*teamloader.LoadResult, error)
	loadMu   sync.Mutex

	mu           sync.Mutex
	stopped      bool
	constructing sync.WaitGroup
	owned        map[*Session]struct{}
}

var _ acp.Agent = (*Agent)(nil)

// Session represents an ACP session.
type Session struct {
	id             string
	sess           *session.Session
	rt             runtime.Runtime
	team           *team.Team
	workingDir     string
	additionalDirs []string

	mu sync.Mutex

	turns       chan struct{}
	cancel      context.CancelFunc
	generation  uint64
	closed      bool
	cleanupDone chan struct{}
}

var errSessionClosed = errors.New("ACP session closed")

func (s *Session) cancelTurn() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) close(ctx context.Context) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupDone != nil {
		return s.cleanupDone
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.initTurns()
	s.cleanupDone = make(chan struct{})
	go func() {
		defer close(s.cleanupDone)
		// The admitted turn drains runtime events before returning the token.
		<-s.turns
		defer func() { s.turns <- struct{}{} }()
		if s.rt != nil {
			if err := s.rt.Close(); err != nil {
				slog.ErrorContext(ctx, "Failed to close ACP runtime", "session_id", s.id, "error", err)
			}
		}
		if s.team != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := s.team.StopToolSets(cleanupCtx); err != nil {
				slog.ErrorContext(ctx, "Failed to stop ACP toolsets", "session_id", s.id, "error", err)
			}
		}
	}()
	return s.cleanupDone
}

// initTurns is called with s.mu held.
func (s *Session) initTurns() {
	if s.turns == nil {
		s.turns = make(chan struct{}, 1)
		s.turns <- struct{}{}
	}
}

func (s *Session) startTurn(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	turnCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return nil, nil, errSessionClosed
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		cancel()
		return nil, nil, err
	}
	s.initTurns()
	turns := s.turns
	previous := s.cancel
	s.generation++
	generation := s.generation
	s.cancel = cancel
	s.mu.Unlock()

	if previous != nil {
		previous()
	}

	select {
	case <-turnCtx.Done():
		s.clearTurn(generation, cancel)
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, nil, errSessionClosed
		}
		return nil, nil, turnCtx.Err()
	case <-turns:
	}

	s.mu.Lock()
	closed := s.closed
	current := s.generation == generation
	err := turnCtx.Err()
	s.mu.Unlock()
	if closed || !current || err != nil {
		turns <- struct{}{}
		s.clearTurn(generation, cancel)
		if closed {
			return nil, nil, errSessionClosed
		}
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, context.Canceled
	}

	finish := func() {
		turns <- struct{}{}
		s.clearTurn(generation, cancel)
	}
	return turnCtx, finish, nil
}

func (s *Session) clearTurn(generation uint64, cancel context.CancelFunc) {
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation {
		s.cancel = nil
	}
}

// NewAgent creates a new ACP agent.
func NewAgent(agentSource config.Source, runConfig *config.RuntimeConfig, sessionStore session.Store) *Agent {
	a := &Agent{
		agentSource:  agentSource,
		runConfig:    runConfig,
		sessionStore: sessionStore,
		sessions:     make(map[string]*Session),
		owned:        make(map[*Session]struct{}),
	}
	a.loadTeam = func(ctx context.Context, workingDir string) (*teamloader.LoadResult, error) {
		opts := append(loaderdefaults.Opts(),
			teamloader.WithToolsetRegistry(createToolsetRegistry(a)),
			teamloader.WithWorkingDir(workingDir),
		)
		return teamloader.LoadWithConfig(ctx, a.agentSource, a.runConfig, opts...)
	}
	return a
}

// Stop stops the agent and its session-owned runtimes and toolsets.
func (a *Agent) Stop(ctx context.Context) {
	a.mu.Lock()
	a.stopped = true
	owned := make([]*Session, 0, len(a.owned))
	for s := range a.owned {
		owned = append(owned, s)
	}
	validationTeam := a.team
	a.team = nil
	clear(a.sessions)
	a.mu.Unlock()

	for _, s := range owned {
		s.close(ctx)
	}
	// Constructors rejected at registration clean up before leaving this group.
	a.constructing.Wait()
	for _, s := range owned {
		<-s.close(ctx)
	}
	if validationTeam != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := validationTeam.StopToolSets(cleanupCtx); err != nil {
			slog.ErrorContext(ctx, "Failed to stop tool sets", "error", err)
		}
	}
}

// SetAgentConnection sets the ACP connection.
func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) {
	a.conn = conn
}

// Initialize implements [acp.Agent].
func (a *Agent) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	slog.DebugContext(ctx, "ACP Initialize called", "client_version", params.ProtocolVersion)

	a.mu.Lock()
	a.clientFS = params.ClientCapabilities.Fs
	defer a.mu.Unlock()
	if a.stopped {
		return acp.InitializeResponse{}, errors.New("agent stopped")
	}
	if a.team != nil {
		return acp.InitializeResponse{}, errors.New("agent already initialized")
	}
	loadResult, err := a.loadTeam(ctx, a.defaultWorkingDir())
	if err != nil {
		return acp.InitializeResponse{}, fmt.Errorf("failed to load teams: %w", err)
	}
	t := loadResult.Team
	a.team = t
	slog.DebugContext(ctx, "Teams loaded successfully", "source", a.agentSource.Name(), "agent_count", t.Size())

	agentTitle := "docker agent"
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    "docker agent",
			Version: version.Version,
			Title:   &agentTitle,
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: false,
			SessionCapabilities: acp.SessionCapabilities{
				AdditionalDirectories: &acp.SessionAdditionalDirectoriesCapabilities{},
				Close:                 &acp.SessionCloseCapabilities{},
				List:                  &acp.SessionListCapabilities{},
				Resume:                &acp.SessionResumeCapabilities{},
			},
			PromptCapabilities: acp.PromptCapabilities{
				EmbeddedContext: true,
				Image:           true,
				Audio:           false, // Not yet supported
			},
			McpCapabilities: acp.McpCapabilities{
				Http: false, // MCP servers from client not yet supported
				Sse:  false, // MCP servers from client not yet supported
			},
		},
	}, nil
}

// beginSessionConstruction prevents shutdown from overlooking an in-flight load.
func (a *Agent) beginSessionConstruction() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return errors.New("agent stopped")
	}
	if a.team == nil {
		return errors.New("agent not initialized")
	}
	a.constructing.Add(1)
	return nil
}

// newRuntime creates a session-owned team and runtime using the default agent.
func (a *Agent) newRuntime(ctx context.Context, workingDir string) (*Session, *agent.Agent, error) {
	workingDir = cmp.Or(workingDir, a.defaultWorkingDir())
	// Source.Read and EncryptedConfig must belong to the same load.
	a.loadMu.Lock()
	loadResult, err := a.loadTeam(ctx, workingDir)
	a.loadMu.Unlock()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load session team: %w", err)
	}
	acpSess := &Session{team: loadResult.Team}
	defaultAgent, err := acpSess.team.DefaultAgent()
	if err != nil {
		<-acpSess.close(ctx)
		return nil, nil, fmt.Errorf("failed to resolve default agent: %w", err)
	}

	rt, err := runtime.New(ctx, acpSess.team,
		runtime.WithCurrentAgent(defaultAgent.Name()),
		runtime.WithSessionStore(a.sessionStore),
		runtime.WithProviderRegistry(loadResult.ProviderRegistry),
		runtime.WithWorkingDir(workingDir),
		// Match the CLI tracer scope so runtime spans remain enabled in ACP mode.
		runtime.WithTracer(otel.Tracer(version.AppName)),
	)
	if err != nil {
		<-acpSess.close(ctx)
		return nil, nil, err
	}
	acpSess.rt = rt
	return acpSess, defaultAgent, nil
}

// registerSessionIfAbsent transfers ownership only if this session wins registration.
func (a *Agent) registerSessionIfAbsent(acpSess *Session) (*Session, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return nil, false, errors.New("agent stopped")
	}
	if existing, ok := a.sessions[acpSess.id]; ok {
		return existing, false, nil
	}
	a.sessions[acpSess.id] = acpSess
	a.owned[acpSess] = struct{}{}
	return acpSess, true, nil
}

// NewSession implements [acp.Agent].
func (a *Agent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	slog.DebugContext(ctx, "ACP NewSession called", "cwd", params.Cwd)

	if len(params.McpServers) > 0 {
		slog.WarnContext(ctx, "MCP servers provided by client are not yet supported", "count", len(params.McpServers))
	}

	workingDir, err := resolveWorkingDir(params.Cwd)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	// Direct Go callers retain the legacy empty-cwd fallback; the SDK requires
	// cwd on the wire. Keep fallback execution paths out of saved provenance.
	if err := validateWorkingDir(workingDir); err != nil {
		return acp.NewSessionResponse{}, err
	}

	additionalDirs, err := resolveAdditionalDirectories(params.AdditionalDirectories)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	if err := a.beginSessionConstruction(); err != nil {
		return acp.NewSessionResponse{}, err
	}
	defer a.constructing.Done()
	acpSess, defaultAgent, err := a.newRuntime(ctx, workingDir)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	stored := false
	defer func() {
		if !stored {
			<-acpSess.close(ctx)
		}
	}()

	sess := session.New(
		session.WithMaxIterations(defaultAgent.MaxIterations()),
		session.WithMaxConsecutiveToolCalls(defaultAgent.MaxConsecutiveToolCalls()),
		session.WithMaxOldToolCallTokens(defaultAgent.MaxOldToolCallTokens()),
		session.WithMaxToolResultTokens(defaultAgent.MaxToolResultTokens()),
		session.WithWorkingDir(workingDir),
	)
	sess.SetTitle("ACP Session " + sess.ID)

	if err := a.sessionStore.AddSession(ctx, sess); err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("failed to persist session: %w", err)
	}

	slog.DebugContext(ctx, "ACP session created", "session_id", sess.ID)

	acpSess.id = sess.ID
	acpSess.sess = sess
	acpSess.workingDir = workingDir
	acpSess.additionalDirs = additionalDirs
	_, stored, err = a.registerSessionIfAbsent(acpSess)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	if !stored {
		return acp.NewSessionResponse{}, session.ErrAlreadyExists
	}

	return acp.NewSessionResponse{SessionId: acp.SessionId(sess.ID)}, nil
}

// Authenticate implements [acp.Agent].
func (a *Agent) Authenticate(ctx context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	slog.DebugContext(ctx, "ACP Authenticate called")
	return acp.AuthenticateResponse{}, nil
}

// Logout implements [acp.Agent] (optional, not supported).
func (a *Agent) Logout(ctx context.Context, _ acp.LogoutRequest) (acp.LogoutResponse, error) {
	slog.DebugContext(ctx, "ACP Logout called (not supported)")
	return acp.LogoutResponse{}, acp.NewMethodNotFound(acp.AgentMethodLogout)
}

// LoadSession implements [acp.AgentLoader] (optional, not supported).
func (a *Agent) LoadSession(ctx context.Context, _ acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	slog.DebugContext(ctx, "ACP LoadSession called (not supported)")
	return acp.LoadSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionLoad)
}

// CloseSession implements [acp.Agent].
func (a *Agent) CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	sid := string(params.SessionId)
	slog.DebugContext(ctx, "ACP CloseSession called", "session_id", sid)

	a.mu.Lock()
	acpSess, ok := a.sessions[sid]
	if ok {
		delete(a.sessions, sid)
	}
	a.mu.Unlock()

	if ok && acpSess != nil {
		done := acpSess.close(ctx)
		go func() {
			<-done
			a.mu.Lock()
			defer a.mu.Unlock()
			delete(a.owned, acpSess)
		}()
	}

	return acp.CloseSessionResponse{}, nil
}

// ListSessions implements [acp.Agent].
func (a *Agent) ListSessions(ctx context.Context, _ acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	slog.DebugContext(ctx, "ACP ListSessions called")

	summaries, err := a.sessionStore.GetSessionSummaries(ctx)
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("failed to list sessions: %w", err)
	}

	sessions := make([]acp.SessionInfo, 0, len(summaries))
	for _, s := range summaries {
		cwd, additionalDirs := a.sessionListPaths(ctx, s.ID)
		info := acp.SessionInfo{
			SessionId:             acp.SessionId(s.ID),
			Title:                 &s.Title,
			Cwd:                   cwd,
			AdditionalDirectories: additionalDirs,
		}
		if !s.CreatedAt.IsZero() {
			// We don't track session updates yet, so report CreatedAt in
			// the ACP UpdatedAt field as our best-effort timestamp.
			createdAt := s.CreatedAt.UTC().Format(time.RFC3339)
			info.UpdatedAt = &createdAt
		}
		sessions = append(sessions, info)
	}

	return acp.ListSessionsResponse{Sessions: sessions}, nil
}

// ResumeSession implements [acp.Agent].
func (a *Agent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	sid := string(params.SessionId)
	slog.DebugContext(ctx, "ACP ResumeSession called", "session_id", sid)
	if err := ctx.Err(); err != nil {
		return acp.ResumeSessionResponse{}, err
	}

	workingDir, err := resolveWorkingDir(params.Cwd)
	if err != nil {
		return acp.ResumeSessionResponse{}, acp.NewInvalidParams(err.Error())
	}
	additionalDirs, err := resolveAdditionalDirectories(params.AdditionalDirectories)
	if err != nil {
		return acp.ResumeSessionResponse{}, acp.NewInvalidParams(err.Error())
	}

	a.mu.Lock()
	existing := a.sessions[sid]
	a.mu.Unlock()
	if existing != nil {
		return acp.ResumeSessionResponse{}, a.resumeRegisteredSession(ctx, existing, workingDir, additionalDirs)
	}

	sess, err := a.sessionStore.GetSession(ctx, sid)
	if err != nil {
		return acp.ResumeSessionResponse{}, fmt.Errorf("failed to load session %s: %w", sid, err)
	}
	if err := validateResumeWorkingDir(sess.WorkingDir, workingDir); err != nil {
		return acp.ResumeSessionResponse{}, acp.NewInvalidParams(err.Error())
	}

	if err := a.beginSessionConstruction(); err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	defer a.constructing.Done()
	acpSess, _, err := a.newRuntime(ctx, sess.WorkingDir)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}

	if err := ctx.Err(); err != nil {
		<-acpSess.close(ctx)
		return acp.ResumeSessionResponse{}, err
	}
	acpSess.id = sid
	acpSess.sess = sess
	acpSess.workingDir = sess.WorkingDir
	acpSess.additionalDirs = additionalDirs
	existing, stored, err := a.registerSessionIfAbsent(acpSess)
	if !stored {
		<-acpSess.close(ctx)
	}
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	if !stored {
		return acp.ResumeSessionResponse{}, a.resumeRegisteredSession(ctx, existing, workingDir, additionalDirs)
	}

	slog.DebugContext(ctx, "ACP session resumed", "session_id", sid)

	return acp.ResumeSessionResponse{}, nil
}

// SetSessionConfigOption implements [acp.Agent] (optional, not advertised in capabilities).
func (a *Agent) SetSessionConfigOption(ctx context.Context, _ acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	slog.DebugContext(ctx, "ACP SetSessionConfigOption called (not supported)")
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}

// Cancel implements [acp.Agent].
func (a *Agent) Cancel(_ context.Context, params acp.CancelNotification) error {
	sid := string(params.SessionId)
	slog.Debug("ACP Cancel called", "session_id", sid)

	a.mu.Lock()
	acpSess, ok := a.sessions[sid]
	a.mu.Unlock()

	if ok && acpSess != nil {
		acpSess.cancelTurn()
	}

	return nil
}

// Prompt implements [acp.Agent].
func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	sid := string(params.SessionId)
	slog.DebugContext(ctx, "ACP Prompt called", "session_id", sid)

	a.mu.Lock()
	acpSess, ok := a.sessions[sid]
	a.mu.Unlock()

	if !ok {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", sid)
	}

	turnCtx, finish, err := acpSess.startTurn(ctx)
	if err != nil {
		if errors.Is(err, errSessionClosed) {
			return acp.PromptResponse{}, fmt.Errorf("session %s not found", sid)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		return acp.PromptResponse{}, err
	}
	defer finish()

	userMsg := a.buildUserMessage(turnCtx, sid, params.Prompt)
	if userMsg != nil && (userMsg.Message.Content != "" || len(userMsg.Message.MultiContent) > 0) {
		acpSess.sess.AddMessage(userMsg)
	}

	if err := a.runAgent(turnCtx, acpSess); err != nil {
		if turnCtx.Err() != nil {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		return acp.PromptResponse{}, err
	}

	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// buildUserContent constructs user message text from ACP content blocks.
func (a *Agent) buildUserContent(ctx context.Context, sessionID string, prompt []acp.ContentBlock) string {
	msg := a.buildUserMessage(ctx, sessionID, prompt)
	if msg == nil {
		return ""
	}
	return msg.Message.Content
}

func (a *Agent) buildUserMessage(ctx context.Context, sessionID string, prompt []acp.ContentBlock) *session.Message {
	var (
		parts          []string
		multiContent   []chat.MessagePart
		hasRichContent bool
	)

	appendText := func(text string) {
		if text == "" {
			return
		}
		parts = append(parts, text)
		multiContent = append(multiContent, chat.MessagePart{Type: chat.MessagePartTypeText, Text: text})
	}

	for _, content := range prompt {
		switch {
		case content.Text != nil:
			appendText(content.Text.Text)

		case content.ResourceLink != nil:
			rl := content.ResourceLink
			slog.DebugContext(ctx, "Processing resource link", "uri", rl.Uri, "name", rl.Name)

			if fileContent, ok := a.readResourceLink(ctx, sessionID, rl); ok {
				appendText(fmt.Sprintf("\n\n--- File: %s ---\n%s\n--- End File ---\n", resourceLinkName(rl), fileContent))
			} else {
				appendText(fmt.Sprintf("\n[Referenced file: %s (content unavailable)]\n", resourceLinkName(rl)))
			}

		case content.Resource != nil:
			res := content.Resource.Resource
			if res.TextResourceContents != nil {
				slog.DebugContext(ctx, "Processing embedded text resource", "uri", res.TextResourceContents.Uri)
				appendText(fmt.Sprintf("\n\n--- Resource: %s ---\n%s\n--- End Resource ---\n",
					res.TextResourceContents.Uri, res.TextResourceContents.Text))
			} else if res.BlobResourceContents != nil {
				slog.DebugContext(ctx, "Processing embedded blob resource", "uri", res.BlobResourceContents.Uri)
				appendText(fmt.Sprintf("\n[Binary resource: %s (type: %s)]\n",
					res.BlobResourceContents.Uri, stringOrDefault(res.BlobResourceContents.MimeType, "unknown")))
			}

		case content.Image != nil:
			img := content.Image
			slog.DebugContext(ctx, "Processing image content", "mime_type", img.MimeType)
			hasRichContent = true
			multiContent = append(multiContent, chat.MessagePart{
				Type: chat.MessagePartTypeImageURL,
				ImageURL: &chat.MessageImageURL{
					URL:    imageDataURL(img.MimeType, img.Data),
					Detail: chat.ImageURLDetailAuto,
				},
			})

		case content.Audio != nil:
			slog.DebugContext(ctx, "Audio content received but not yet supported")
			appendText("[Audio content provided]")
		}
	}

	content := strings.Join(parts, "")
	if !hasRichContent {
		return session.UserMessage(content)
	}
	return session.UserMessage(content, multiContent...)
}

// readResourceLink attempts to read a text file referenced by an ACP resource link.
func (a *Agent) readResourceLink(ctx context.Context, sessionID string, rl *acp.ContentBlockResourceLink) (string, bool) {
	if !a.supportsClientReadTextFile() {
		slog.DebugContext(ctx, "ACP client does not support reading resource links")
		return "", false
	}

	path, ok := resourceLinkPath(rl.Uri)
	if !ok {
		slog.DebugContext(ctx, "Unsupported ACP resource link URI", "uri", rl.Uri)
		return "", false
	}

	resolvedPath, err := a.resolveSessionPath(sessionID, path)
	if err != nil {
		slog.WarnContext(ctx, "Blocked unsafe file resource link", "path", path, "error", err)
		return "", false
	}

	resp, err := a.conn.ReadTextFile(ctx, acp.ReadTextFileRequest{
		SessionId: acp.SessionId(sessionID),
		Path:      resolvedPath,
	})
	if err != nil {
		slog.DebugContext(ctx, "Failed to read resource link", "path", resolvedPath, "error", err)
		return "", false
	}

	return resp.Content, true
}

func resourceLinkName(rl *acp.ContentBlockResourceLink) string {
	if name := chat.SanitizeDisplayName(rl.Name); name != "" {
		return name
	}
	if path, ok := resourceLinkPath(rl.Uri); ok {
		if base := chat.SanitizeDisplayName(filepath.Base(path)); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "resource"
}

func resourceLinkPath(rawURI string) (string, bool) {
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme == "" {
		return rawURI, rawURI != ""
	}
	if u.Scheme != "file" {
		return "", false
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", false
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", false
	}
	return path, path != ""
}

func imageDataURL(mimeType, data string) string {
	if strings.HasPrefix(data, "data:") {
		return data
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, data)
}

func stringOrDefault(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

// SetSessionMode implements acp.Agent (optional).
func (a *Agent) SetSessionMode(ctx context.Context, _ acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	slog.DebugContext(ctx, "ACP SetSessionMode called (not supported)")
	return acp.SetSessionModeResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetMode)
}

// sendUpdate sends a session update notification to the ACP client.
func (a *Agent) sendUpdate(ctx context.Context, sessionID string, update acp.SessionUpdate) error {
	return a.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: acp.SessionId(sessionID),
		Update:    update,
	})
}

// runAgent runs a single agent loop and streams updates to the ACP client.
func (a *Agent) runAgent(ctx context.Context, acpSess *Session) error {
	slog.DebugContext(ctx, "Running agent turn", "session_id", acpSess.id)

	ctx = withSessionID(ctx, acpSess.id)

	if err := a.emitAvailableCommands(ctx, acpSess); err != nil {
		slog.DebugContext(ctx, "Failed to emit available commands", "error", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	eventsChan := acpSess.rt.RunStream(runCtx, acpSess.sess)
	// Cancel on handler errors too, before waiting for runtime teardown.
	defer func() {
		cancel()
		for range eventsChan {
		}
	}()
	toolCallArgs := map[string]string{}

	for event := range eventsChan {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		switch e := event.(type) {
		case *runtime.AgentChoiceEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.UpdateAgentMessageText(e.Content)); err != nil {
				return err
			}

		case *runtime.AgentChoiceReasoningEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.UpdateAgentThoughtText(e.Content)); err != nil {
				return err
			}

		case *runtime.ToolCallConfirmationEvent:
			if err := a.handleToolCallConfirmation(ctx, acpSess, e); err != nil {
				return err
			}

		case *runtime.ToolCallEvent:
			toolCallArgs[e.ToolCall.ID] = e.ToolCall.Function.Arguments
			if err := a.sendUpdate(ctx, acpSess.id, buildToolCallStart(e.ToolCall, e.ToolDefinition)); err != nil {
				return err
			}

		case *runtime.ToolCallResponseEvent:
			args, ok := toolCallArgs[e.ToolCallID]
			if !ok {
				return fmt.Errorf("missing tool call arguments for tool call ID %s", e.ToolCallID)
			}
			delete(toolCallArgs, e.ToolCallID)

			if err := a.sendUpdate(ctx, acpSess.id, buildToolCallComplete(args, e)); err != nil {
				return err
			}

			if isTodoTool(e.ToolDefinition.Name) && e.Result != nil && e.Result.Meta != nil {
				if planUpdate := buildPlanUpdateFromTodos(e.Result.Meta); planUpdate != nil {
					if err := a.sendUpdate(ctx, acpSess.id, *planUpdate); err != nil {
						return err
					}
				}
			}

		case *runtime.ErrorEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.UpdateAgentMessageText(fmt.Sprintf("\n\nError: %s\n", e.Error))); err != nil {
				return err
			}

		case *runtime.WarningEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.UpdateAgentMessageText(fmt.Sprintf("\nWarning: %s\n", e.Message))); err != nil {
				return err
			}

		case *runtime.SessionTitleEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.SessionUpdate{
				SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
					SessionUpdate: "session_info_update",
					Title:         &e.Title,
				},
			}); err != nil {
				return err
			}

		case *runtime.TokenUsageEvent:
			if e.Usage != nil {
				usageUpdate := acp.SessionUsageUpdate{
					SessionUpdate: "usage_update",
					Size:          int(e.Usage.ContextLimit),
					Used:          int(e.Usage.ContextLength),
				}
				if e.Usage.Cost > 0 {
					usageUpdate.Cost = &acp.Cost{
						Amount:   e.Usage.Cost,
						Currency: "USD",
					}
				}
				if err := a.sendUpdate(ctx, acpSess.id, acp.SessionUpdate{UsageUpdate: &usageUpdate}); err != nil {
					return err
				}
			}

		case *runtime.ModelFallbackEvent:
			if err := a.sendUpdate(ctx, acpSess.id, acp.UpdateAgentMessageText(
				fmt.Sprintf("\nModel %s failed, falling back to %s (%s)\n", e.FailedModel, e.FallbackModel, e.Reason),
			)); err != nil {
				return err
			}

		case *runtime.MaxIterationsReachedEvent:
			if err := a.handleMaxIterationsReached(ctx, acpSess, e); err != nil {
				return err
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// handleToolCallConfirmation handles tool call permission requests.
func (a *Agent) handleToolCallConfirmation(ctx context.Context, acpSess *Session, e *runtime.ToolCallConfirmationEvent) error {
	toolCallUpdate := buildToolCallUpdate(e.ToolCall, e.ToolDefinition, acp.ToolCallStatusPending)

	permResp, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: acp.SessionId(acpSess.id),
		ToolCall:  toolCallUpdate,
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Allow this action",
				OptionId: "allow",
			},
			{
				Kind:     acp.PermissionOptionKindAllowAlways,
				Name:     "Always allow this tool for this session",
				OptionId: "allow-always",
			},
			{
				Kind:     acp.PermissionOptionKindRejectOnce,
				Name:     "Skip this action",
				OptionId: "reject",
			},
		},
	})
	if err != nil {
		return err
	}

	if permResp.Outcome.Cancelled != nil {
		acpSess.rt.Resume(ctx, runtime.ResumeRequest{Type: runtime.ResumeTypeReject})
		return nil
	}

	if permResp.Outcome.Selected == nil {
		return errors.New("unexpected permission outcome")
	}

	switch string(permResp.Outcome.Selected.OptionId) {
	case "allow":
		acpSess.rt.Resume(ctx, runtime.ResumeRequest{Type: runtime.ResumeTypeApprove})
	case "allow-always":
		acpSess.rt.Resume(ctx, runtime.ResumeApproveTool(e.ToolCall.Function.Name))
	case "reject":
		acpSess.rt.Resume(ctx, runtime.ResumeRequest{Type: runtime.ResumeTypeReject})
	default:
		return fmt.Errorf("unexpected permission option: %s", permResp.Outcome.Selected.OptionId)
	}

	return nil
}

// handleMaxIterationsReached handles max iterations events.
func (a *Agent) handleMaxIterationsReached(ctx context.Context, acpSess *Session, e *runtime.MaxIterationsReachedEvent) error {
	title := fmt.Sprintf("Maximum iterations (%d) reached", e.MaxIterations)
	permResp, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: acp.SessionId(acpSess.id),
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "max_iterations",
			Title:      &title,
			Kind:       acp.Ptr(acp.ToolKindExecute),
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Continue",
				OptionId: "continue",
			},
			{
				Kind:     acp.PermissionOptionKindRejectOnce,
				Name:     "Stop",
				OptionId: "stop",
			},
		},
	})
	if err != nil {
		return err
	}

	if permResp.Outcome.Cancelled != nil || permResp.Outcome.Selected == nil ||
		string(permResp.Outcome.Selected.OptionId) != "continue" {
		acpSess.rt.Resume(ctx, runtime.ResumeRequest{Type: runtime.ResumeTypeReject})
	} else {
		acpSess.rt.Resume(ctx, runtime.ResumeRequest{Type: runtime.ResumeTypeApprove})
	}

	return nil
}

// emitAvailableCommands sends the list of available slash commands to the client.
func (a *Agent) emitAvailableCommands(ctx context.Context, acpSess *Session) error {
	return a.sendUpdate(ctx, acpSess.id, acp.SessionUpdate{
		AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acp.AvailableCommand{
				{Name: "new", Description: "Clear session history and start fresh"},
				{Name: "compact", Description: "Generate summary and compact session history"},
				{Name: "usage", Description: "Display token usage statistics"},
			},
		},
	})
}

func (a *Agent) supportsClientReadTextFile() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientFS.ReadTextFile && a.conn != nil
}

func (a *Agent) supportsClientWriteTextFile() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientFS.WriteTextFile && a.conn != nil
}

func (a *Agent) resolveSessionPath(sessionID, userPath string) (string, error) {
	a.mu.Lock()
	acpSess := a.sessions[sessionID]
	a.mu.Unlock()
	if acpSess == nil {
		return "", fmt.Errorf("session %s not found", sessionID)
	}

	workingDir, roots := acpSess.pathRoots(a.defaultWorkingDir())
	return resolvePathInRoots(userPath, workingDir, roots)
}

func (a *Agent) sessionListPaths(ctx context.Context, sessionID string) (string, []string) {
	a.mu.Lock()
	acpSess := a.sessions[sessionID]
	a.mu.Unlock()
	if acpSess != nil {
		cwd, additionalDirs := acpSess.workspaceSnapshot()
		return cmp.Or(cwd, a.defaultWorkingDir()), additionalDirs
	}

	cwd := a.defaultWorkingDir()
	if a.sessionStore != nil {
		if sess, err := a.sessionStore.GetSession(ctx, sessionID); err == nil && sess.WorkingDir != "" {
			cwd = sess.WorkingDir
		}
	}
	return cwd, nil
}

func (a *Agent) defaultWorkingDir() string {
	if a.runConfig != nil && a.runConfig.WorkingDir != "" {
		if wd, err := resolveWorkingDir(a.runConfig.WorkingDir); err == nil {
			return wd
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	wd, err := resolveWorkingDir(cwd)
	if err != nil {
		return cwd
	}
	return wd
}

func (s *Session) workspaceSnapshot() (string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workingDir := s.workingDir
	if workingDir == "" && s.sess != nil {
		workingDir = s.sess.WorkingDir
	}
	return workingDir, slices.Clone(s.additionalDirs)
}

func (s *Session) pathRoots(fallbackWorkingDir string) (string, []string) {
	workingDir, additionalDirs := s.workspaceSnapshot()
	workingDir = cmp.Or(workingDir, fallbackWorkingDir)

	roots := make([]string, 0, 1+len(additionalDirs))
	if workingDir != "" {
		roots = append(roots, workingDir)
	}
	roots = append(roots, additionalDirs...)
	return workingDir, dedupePaths(roots)
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		key := normalizePathForComparison(filepath.Clean(path))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, filepath.Clean(path))
	}
	return result
}

// resolveWorkingDir normalizes a working directory path.
func resolveWorkingDir(cwd string) (string, error) {
	wd := strings.TrimSpace(cwd)
	if wd == "" {
		return "", nil
	}
	absWd, err := filepath.Abs(wd)
	if err != nil {
		return "", fmt.Errorf("invalid working directory: %w", err)
	}
	return filepath.Clean(absWd), nil
}

func validateWorkingDir(workingDir string) error {
	if workingDir == "" {
		return nil
	}
	info, err := os.Stat(workingDir)
	if err != nil {
		return fmt.Errorf("working directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return errors.New("working directory must be a directory")
	}
	return nil
}

func resolveAdditionalDirectories(dirs []string) ([]string, error) {
	resolved := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("additional directory must be absolute: %s", dir)
		}
		absDir, err := resolveWorkingDir(dir)
		if err != nil {
			return nil, err
		}
		if err := validateWorkingDir(absDir); err != nil {
			return nil, fmt.Errorf("invalid additional directory %q: %w", dir, err)
		}
		resolved = append(resolved, absDir)
	}
	return dedupePaths(resolved), nil
}
