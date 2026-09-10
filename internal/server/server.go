// Package server wires the Telegram backends to the MCP tool surface and runs
// the stdio transport.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/config"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/credstore"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/tools"
)

// Name and Version identify this server in the MCP handshake.
const Name = "better-telegram-mcp"

// Version is stamped at build time (-ldflags "-X ...Version=..."); the default
// says the binary was built without one.
var Version = "dev"

// Server holds the process-wide state the tools read: the connected backend,
// what the credentials looked like, and the runtime limits `config` edits.
type Server struct {
	settings *config.Settings
	store    *credstore.Store

	mu          sync.RWMutex
	backend     telegram.Backend
	configured  bool
	pendingAuth bool
	runtime     map[string]int

	// connecting and connErr describe the background connect: whether one is
	// still in flight and what the last attempt failed with. A tool called
	// before the backend is up answers from these instead of a bare "not
	// connected".
	connecting bool
	connErr    error
}

// New resolves credentials and prepares the server. It does not connect yet:
// Run does that, so a caller can inspect the resolved state first.
func New() (*Server, error) {
	settings, err := config.Load()
	if err != nil {
		return nil, err
	}
	store := credstore.New(settings.DataDir)

	if !settings.IsConfigured() {
		saved, err := store.Load()
		if err != nil {
			// A blob written under a different master secret is a real problem,
			// but not one that should stop a server whose env is complete.
			slog.Warn("ignoring the saved credentials", "error", err)
		} else if saved != nil {
			settings.ApplySaved(saved)
		}
	}

	return &Server{
		settings:   settings,
		store:      store,
		configured: settings.IsConfigured(),
		runtime:    map[string]int{"message_limit": 20, "timeout": 30},
	}, nil
}

// Settings exposes the resolved configuration (the CLI prints parts of it).
func (s *Server) Settings() *config.Settings { return s.settings }

// Connect brings up the backend the credentials describe. An unconfigured
// server still runs: help and config stay available and every other tool
// explains how to configure it.
func (s *Server) Connect(ctx context.Context) error {
	if !s.settings.IsConfigured() {
		slog.Warn("no Telegram credentials configured; " +
			"help and config are available, other tools will show setup instructions")
		return nil
	}

	backend, err := s.newBackend()
	if err != nil {
		return err
	}
	if err := backend.Connect(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.backend = backend
	s.configured = true
	s.pendingAuth = s.settings.Mode == config.ModeUser && !backend.IsAuthorized(ctx)
	s.mu.Unlock()

	slog.Info("connected to Telegram", "mode", string(s.settings.Mode))
	if s.PendingAuth() {
		slog.Warn("the saved session is not signed in; " +
			"run `better-telegram-mcp auth --phone +<number>` to complete it")
	}
	return nil
}

// StartConnect brings the backend up in the background.
//
// The MCP handshake must not wait for Telegram. This server is spawned by a
// supervisor that expects an initialize response in seconds, and MTProto can
// take much longer than that -- or, during an outage, never answer at all.
// Connecting inline is what turned one unreachable account into a server that
// never spoke MCP, which its supervisor read as a crash, restarted, and
// restarted again; every other Telegram tool in the same namespace went with
// it. So the transport comes up first and the connection catches up, retrying
// with backoff so an outage heals by itself.
func (s *Server) StartConnect(ctx context.Context) {
	if !s.settings.IsConfigured() {
		slog.Warn("no Telegram credentials configured; " +
			"help and config are available, other tools will show setup instructions")
		return
	}

	s.mu.Lock()
	s.connecting = true
	s.mu.Unlock()

	go s.connectLoop(ctx)
}

// connectRetryCap is the longest gap between attempts. Long enough not to add
// to the load on a datacenter that is already refusing, short enough that a
// recovered account is usable again without anyone restarting anything.
const connectRetryCap = 2 * time.Minute

func (s *Server) connectLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for attempt := 1; ; attempt++ {
		err := s.Connect(ctx)
		if err == nil {
			s.mu.Lock()
			s.connecting = false
			s.connErr = nil
			s.mu.Unlock()
			return
		}

		s.mu.Lock()
		s.connErr = err
		s.mu.Unlock()

		// A session another process holds is not a transient failure: retrying
		// would only keep the operator from seeing the one thing that fixes it.
		var busy *telegram.SessionBusyError
		if errors.As(err, &busy) || ctx.Err() != nil {
			s.mu.Lock()
			s.connecting = false
			s.mu.Unlock()
			slog.Error("giving up on the Telegram connection", "error", err)
			return
		}

		slog.Warn("could not connect to Telegram; will retry",
			"attempt", attempt, "retry_in", backoff, "error", err)
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.connecting = false
			s.mu.Unlock()
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > connectRetryCap {
			backoff = connectRetryCap
		}
	}
}

// connectStatus describes an absent backend for a caller, or "" when the
// backend's absence is not about the connection.
func (s *Server) connectStatus() string {
	s.mu.RLock()
	connecting, connErr, configured := s.connecting, s.connErr, s.configured
	s.mu.RUnlock()

	switch {
	case connErr != nil && connecting:
		return fmt.Sprintf(
			"Still connecting to Telegram. The last attempt failed: %v. "+
				"The server keeps retrying, so try again in a moment.", connErr)
	case connErr != nil:
		return fmt.Sprintf("Not connected to Telegram: %v", connErr)
	case connecting && configured:
		return "Still connecting to Telegram; try again in a moment."
	}
	return ""
}

func (s *Server) newBackend() (telegram.Backend, error) {
	if s.settings.Mode == config.ModeBot {
		return telegram.NewBotBackend(s.settings.BotToken, telegram.BotOptions{
			CursorPath:    s.settings.CallbackCursorPath(),
			QueuePath:     s.settings.CallbackQueueFile,
			ResumeMapPath: s.settings.ResumeMapFile,
			AliasesPath:   s.settings.AliasesPath(),
			APIBase:       s.settings.APIBase,
		}), nil
	}
	return telegram.NewUserBackend(telegram.UserOptions{
		APIID:         s.settings.APIID,
		APIHash:       s.settings.APIHash,
		SessionPath:   s.settings.SessionPath(),
		AliasesPath:   s.settings.AliasesPath(),
		SessionString: s.settings.SessionString,
		SharedLock:    s.settings.SharedSessionLock,
	}), nil
}

// Close disconnects the backend.
func (s *Server) Close(ctx context.Context) {
	s.mu.Lock()
	backend := s.backend
	s.backend = nil
	s.mu.Unlock()
	if backend != nil {
		_ = backend.Disconnect(ctx)
		slog.Info("disconnected from Telegram")
	}
}

// --- tools.ServerState ---

func (s *Server) Backend() telegram.Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backend
}

func (s *Server) Configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configured
}

func (s *Server) PendingAuth() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingAuth
}

func (s *Server) CredentialState() string {
	if s.Configured() {
		return "configured"
	}
	return "awaiting_setup"
}

func (s *Server) RuntimeConfig() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.runtime))
	for key, value := range s.runtime {
		out[key] = value
	}
	return out
}

func (s *Server) SetRuntimeValue(key string, value int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime[key] = value
}

func (s *Server) ResetCredentials() error {
	if err := s.store.Clear(); err != nil {
		return err
	}
	s.mu.Lock()
	s.configured = false
	s.mu.Unlock()
	return nil
}

// RefreshCredentials re-reads the saved credentials and, when they now describe
// a usable backend, connects it. That is what lets a fresh `auth` run take
// effect without restarting the server.
func (s *Server) RefreshCredentials() error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	if !settings.IsConfigured() {
		saved, err := s.store.Load()
		if err != nil {
			return err
		}
		if saved != nil {
			settings.ApplySaved(saved)
		}
	}
	s.settings = settings

	ctx := context.Background()
	s.Close(ctx)
	s.mu.Lock()
	s.configured = settings.IsConfigured()
	s.pendingAuth = false
	s.mu.Unlock()
	if !settings.IsConfigured() {
		return nil
	}
	return s.Connect(ctx)
}

// ready reports whether a domain tool can run, and the result to return when it
// cannot.
func (s *Server) ready() (telegram.Backend, tools.Result) {
	backend := s.Backend()
	if backend == nil {
		// A configured server whose connection has not come up yet is a
		// different situation from one nobody has set up, and saying so is the
		// difference between "try again" and "go run auth".
		if status := s.connectStatus(); status != "" {
			return nil, tools.Err("%s", status)
		}
		return nil, tools.NotReady(s)
	}
	if s.PendingAuth() {
		return nil, tools.NotReady(s)
	}
	return backend, nil
}

// --- MCP wiring ---

// Run serves the MCP protocol over stdio until the transport closes.
func (s *Server) Run(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    Name,
		Version: Version,
	}, nil)

	s.registerTools(server)
	s.registerResources(server)

	return server.Run(ctx, &mcp.StdioTransport{})
}

func boolPtr(value bool) *bool { return &value }

func (s *Server) registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "message",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Messages",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
		Description: messageDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.MessageArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("message", notReady), nil, nil
		}
		// Operator-configured guardrails apply unless the call overrides them.
		if args.Action == "callbacks" {
			if args.AllowedFromIDs == nil {
				args.AllowedFromIDs = s.settings.AllowedCallbackSenders
			}
			if args.DataPattern == "" {
				args.DataPattern = s.settings.CallbackDataPattern
			}
		}
		return external("message", tools.HandleMessage(ctx, backend, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "chat",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Chats",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
		Description: chatDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.ChatArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("chat", notReady), nil, nil
		}
		return external("chat", tools.HandleChat(ctx, backend, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "media",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Media",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
		Description: mediaDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.MediaArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("media", notReady), nil, nil
		}
		return external("media", tools.HandleMedia(ctx, backend, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "contact",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Contacts",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
		Description: contactDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.ContactArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("contact", notReady), nil, nil
		}
		return external("contact", tools.HandleContact(ctx, backend, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "profile",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Profile",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
		Description: profileDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.ProfileArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("profile", notReady), nil, nil
		}
		return external("profile", tools.HandleProfile(ctx, backend, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "folder",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Folders",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
		Description: folderDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.FolderArgs) (*mcp.CallToolResult, any, error) {
		backend, notReady := s.ready()
		if notReady != nil {
			return external("folder", notReady), nil, nil
		}
		return external("folder", tools.HandleFolder(ctx, backend, args)), nil, nil
	})

	// config and help return the server's own state, never content authored by
	// a Telegram user, so they are not wrapped as untrusted.
	mcp.AddTool(server, &mcp.Tool{
		Name: "config",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Telegram Config",
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
		Description: configDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args tools.ConfigArgs) (*mcp.CallToolResult, any, error) {
		return jsonResult(tools.HandleConfig(ctx, s, args)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "config__open_relay",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Open Telegram setup",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
		Description: "Report how to authenticate this server. This build speaks " +
			"stdio only, so setup is the local `better-telegram-mcp auth` command " +
			"rather than a browser relay.",
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return jsonResult(tools.OpenRelay()), nil, nil
	})

	type helpArgs struct {
		Topic string `json:"topic,omitempty" jsonschema:"telegram|messages|chats|media|contacts|profile|folders|all"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "help",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Telegram Help",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
		Description: "Get full documentation for any topic.\n\n" +
			"Topics: telegram | messages | chats | media | contacts | profile | " +
			"folders | all (default: all)",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args helpArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: tools.HandleHelp(args.Topic)}},
		}, nil, nil
	})
}

func (s *Server) registerResources(server *mcp.Server) {
	for _, topic := range []string{"messages", "chats", "media", "contacts", "profile", "folders"} {
		server.AddResource(&mcp.Resource{
			URI:      "telegram://docs/" + topic,
			Name:     topic,
			MIMEType: "text/markdown",
		}, docResource(topic))
	}
	server.AddResource(&mcp.Resource{
		URI:      "telegram://stats",
		Name:     "all",
		MIMEType: "text/markdown",
	}, docResource("all"))
}

func docResource(topic string) mcp.ResourceHandler {
	return func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     tools.HandleHelp(topic),
			}},
		}, nil
	}
}

// jsonResult renders a plain (trusted) result on both response channels.
func jsonResult(payload tools.Result) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: encode(payload)}},
		StructuredContent: payload,
	}
}

func encode(payload any) string {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "could not encode the result: %v"}`, err)
	}
	return string(raw)
}
