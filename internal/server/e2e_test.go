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

	if !telegram.sawCall("getMe") {
		t.Error("the server should have verified the bot token at startup")
	}

	t.Run("tools are published", func(t *testing.T) {
		list, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("tools/list failed: %v", err)
		}
		if len(list.Tools) != 7 {
			names := make([]string, 0, len(list.Tools))
			for _, tool := range list.Tools {
				names = append(names, tool.Name)
			}
			t.Errorf("expected the seven tools, got %d: %v", len(list.Tools), names)
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
