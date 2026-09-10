package server_test

// This is the test that mirrors deployment: it builds the real binary, spawns
// it the way MetaMCP does (a subprocess speaking MCP over stdin/stdout), and
// drives it with a real MCP client. Everything else in the suite talks to the
// server in-process, which cannot catch a startup that writes to stdout, a
// binary that exits before the handshake, or a tool that only works when the
// transport is in memory.
//
// Telegram itself is a stub reached through TELEGRAM_API_BASE, so the test
// needs no credentials and touches no network.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubTelegram answers the handful of Bot API methods this test drives.
type stubTelegram struct {
	mu    sync.Mutex
	calls []string
}

func (s *stubTelegram) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.calls = append(s.calls, method)
		s.mu.Unlock()

		var result any
		switch method {
		case "getMe":
			result = map[string]any{"id": 1, "is_bot": true, "username": "stub_bot"}
		case "sendMessage":
			var request map[string]any
			_ = json.Unmarshal(body, &request)
			result = map[string]any{
				"message_id": 4711,
				"text":       request["text"],
				"chat":       map[string]any{"id": request["chat_id"]},
			}
		case "getUpdates":
			result = []any{}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false, "description": "Unsupported method: " + method, "error_code": 400,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *stubTelegram) sawCall(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, call := range s.calls {
		if call == method {
			return true
		}
	}
	return false
}

// buildBinary compiles the command under test once per run.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "better-telegram-mcp")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/better-telegram-mcp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("could not build the binary: %v\n%s", err, output)
	}
	return binary
}

func TestBinaryServesMCPOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and spawns it")
	}
	telegram := &stubTelegram{}
	stub := telegram.start(t)
	binary := buildBinary(t)

	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"TELEGRAM_BOT_TOKEN=123456:AAFakeTokenForTheStubServer0123456789",
		"TELEGRAM_API_BASE="+stub.URL,
		"TELEGRAM_DATA_DIR="+t.TempDir(),
	)
	// Anything the server prints to stdout that is not JSON-RPC breaks the
	// transport, so the test would fail on the handshake -- which is the point.
	var stderr strings.Builder
	command.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("the binary did not complete the MCP handshake: %v\nstderr:\n%s", err, stderr.String())
	}
	defer session.Close()

	// The connection is started alongside the transport rather than before it,
	// so the handshake can land first. Wait for the server to report it is up
	// before driving the tools that need it.
	waitConnected(ctx, t, session)

	if !telegram.sawCall("getMe") {
		t.Error("the server should have verified the bot token at startup")
	}

	t.Run("tools are published", func(t *testing.T) {
		list, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("tools/list failed: %v", err)
		}
		const published = 9
		if len(list.Tools) != published {
			names := make([]string, 0, len(list.Tools))
			for _, tool := range list.Tools {
				names = append(names, tool.Name)
			}
			t.Errorf("expected %d tools, got %d: %v", published, len(list.Tools), names)
		}
	})

	t.Run("a message goes out to Telegram", func(t *testing.T) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "message",
			Arguments: map[string]any{
				"action":  "send",
				"chat_id": 375465077,
				"text":    "hello from the Go build",
			},
		})
		if err != nil {
			t.Fatalf("message failed: %v", err)
		}
		body := result.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(body, "4711") {
			t.Errorf("expected the sent message id in the result, got %s", body)
		}
		// Content authored by Telegram users must arrive marked as untrusted.
		if !strings.Contains(body, "<untrusted_message_content>") {
			t.Errorf("the result is missing its XPIA boundary: %s", body)
		}
		if !telegram.sawCall("sendMessage") {
			t.Error("sendMessage never reached the Bot API")
		}
	})

	t.Run("config reports the live connection", func(t *testing.T) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "config",
			Arguments: map[string]any{"action": "status"},
		})
		if err != nil {
			t.Fatalf("config failed: %v", err)
		}
		var status map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &status); err != nil {
			t.Fatalf("status is not JSON: %v", err)
		}
		if status["mode"] != "bot" || status["connected"] != true {
			t.Errorf("expected a connected bot server, got %#v", status)
		}
	})

	t.Run("callbacks reach getUpdates", func(t *testing.T) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "message",
			Arguments: map[string]any{"action": "callbacks"},
		})
		if err != nil {
			t.Fatalf("callbacks failed: %v", err)
		}
		if !telegram.sawCall("getUpdates") {
			t.Error("callbacks never polled Telegram")
		}
		if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, `"count": 0`) {
			t.Errorf("expected no pending presses, got %s", result.Content[0].(*mcp.TextContent).Text)
		}
	})

	t.Run("help needs no credentials", func(t *testing.T) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "help",
			Arguments: map[string]any{"topic": "chats"},
		})
		if err != nil {
			t.Fatalf("help failed: %v", err)
		}
		if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "# Telegram Chats") {
			t.Error("help did not serve its document")
		}
	})
}

// An unconfigured server must exit rather than sit there answering nothing --
// that is what tells an operator, and MetaMCP, that setup is missing.
func TestBinaryExitsWhenUnconfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and spawns it")
	}
	binary := buildBinary(t)

	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"TELEGRAM_DATA_DIR="+t.TempDir(),
		"TELEGRAM_BOT_TOKEN=",
		"TELEGRAM_PHONE=",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("an unconfigured server should exit non-zero")
	}
	if !strings.Contains(string(output), "auth --bot-token") {
		t.Errorf("the exit message should name the command to run, got %s", output)
	}
}

// A client shuts an stdio server down by closing the pipe. That is an ordinary
// stop, and it must not look like a crash: MetaMCP restarts its servers that
// way, and a non-zero exit with an error line makes a healthy server read as a
// failing one in the supervisor's log.
func TestBinaryExitsCleanlyWhenTheClientClosesThePipe(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and spawns it")
	}
	telegram := &stubTelegram{}
	stub := telegram.start(t)
	binary := buildBinary(t)

	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"TELEGRAM_BOT_TOKEN=123456:AAFakeTokenForTheStubServer0123456789",
		"TELEGRAM_API_BASE="+stub.URL,
		"TELEGRAM_DATA_DIR="+t.TempDir(),
	)
	// A complete handshake, then EOF -- exactly what a client does on quit.
	command.Stdin = strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"2025-06-18","capabilities":{},` +
			`"clientInfo":{"name":"probe","version":"1"}}}` + "\n")
	var stderr strings.Builder
	command.Stderr = &stderr
	command.Stdout = io.Discard

	if err := command.Run(); err != nil {
		t.Fatalf("a closed pipe should exit 0, got %v\nstderr:\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "[better-telegram-mcp]") {
		t.Errorf("a clean shutdown should not log an error: %s", stderr.String())
	}
}

// waitConnected blocks until config(status) reports a live backend. The
// connection comes up in the background now, so every test that drives a
// Telegram tool has to wait for it rather than assume it happened before the
// handshake returned.
func waitConnected(ctx context.Context, t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "config",
			Arguments: map[string]any{"action": "status"},
		})
		if err != nil {
			t.Fatalf("config failed: %v", err)
		}
		var status map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &status); err != nil {
			t.Fatalf("status is not JSON: %v", err)
		}
		if status["connected"] == true {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never connected: %#v", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A Telegram that accepts the connection and then says nothing is what took the
// user-mode servers down, and it took the whole namespace with them: the server
// connected before it served, so it never answered the MCP handshake, its
// supervisor read that as a crash, and the healthy bot server in the same
// namespace was never listed either. The transport must come up regardless.
func TestBinaryServesMCPWhileTelegramIsSilent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and spawns it")
	}

	release := make(chan struct{})
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "result": map[string]any{"id": 1, "is_bot": true, "username": "slow_bot"},
		})
	}))
	defer stub.Close()
	defer close(release)

	command := exec.Command(buildBinary(t))
	command.Env = append(os.Environ(),
		"TELEGRAM_BOT_TOKEN=123456:AAFakeTokenForTheStubServer0123456789",
		"TELEGRAM_API_BASE="+stub.URL,
		"TELEGRAM_DATA_DIR="+t.TempDir(),
	)
	var stderr strings.Builder
	command.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	started := time.Now()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("the handshake waited on Telegram: %v\nstderr:\n%s", err, stderr.String())
	}
	defer session.Close()
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Errorf("the handshake took %s; it must not wait on Telegram", elapsed)
	}

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list failed while Telegram was silent: %v", err)
	}
	if len(list.Tools) == 0 {
		t.Error("no tools were published while Telegram was silent")
	}

	// A tool that needs the connection says so, instead of the process dying
	// and taking every other server in the namespace with it.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "message",
		Arguments: map[string]any{"action": "send", "chat_id": 1, "text": "hi"},
	})
	if err != nil {
		t.Fatalf("message failed outright: %v", err)
	}
	body := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(body, "connecting to Telegram") {
		t.Errorf("expected the result to name the pending connection, got %s", body)
	}
}
