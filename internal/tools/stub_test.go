package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// stubBackend records what a tool asked of Telegram and answers with whatever
// the test set up, so the tests here are about argument handling and result
// shaping rather than about the wire.
type stubBackend struct {
	mode telegram.Mode

	sentText    string
	sentOptions telegram.SendOptions
	sentChat    any

	callbacks     []telegram.Map
	consumeCalls  []bool
	answered      []string
	resumeStored  map[int]string
	resumeLookups map[int]string

	err error
}

func newStub() *stubBackend {
	return &stubBackend{
		mode:          telegram.ModeBot,
		resumeStored:  map[int]string{},
		resumeLookups: map[int]string{},
	}
}

func (s *stubBackend) Mode() telegram.Mode               { return s.mode }
func (s *stubBackend) Connect(context.Context) error     { return nil }
func (s *stubBackend) Disconnect(context.Context) error  { return nil }
func (s *stubBackend) IsConnected() bool                 { return true }
func (s *stubBackend) IsAuthorized(context.Context) bool { return true }
func (s *stubBackend) ClearCache(context.Context) error  { return s.err }

func (s *stubBackend) SendMessage(_ context.Context, chatID any, text string, opts telegram.SendOptions) (telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.sentChat, s.sentText, s.sentOptions = chatID, text, opts
	return telegram.Map{"message_id": 42, "text": text}, nil
}

func (s *stubBackend) EditMessage(_ context.Context, _ any, messageID int, text string, _ telegram.EditOptions) (telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return telegram.Map{"message_id": messageID, "text": text}, nil
}

func (s *stubBackend) DeleteMessage(context.Context, any, int) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) ForwardMessage(context.Context, any, any, int) (telegram.Map, error) {
	return telegram.Map{"message_id": 1}, s.err
}

func (s *stubBackend) PinMessage(context.Context, any, int) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) ReactToMessage(context.Context, any, int, string) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) SearchMessages(context.Context, string, any, int) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"message_id": 1}}, nil
}

func (s *stubBackend) GetHistory(context.Context, any, int, int) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"message_id": 1}, {"message_id": 2}}, nil
}

func (s *stubBackend) EditMessageButtons(_ context.Context, _ any, messageID int, _ any) (telegram.Map, error) {
	return telegram.Map{"message_id": messageID, "buttons_replaced": true}, s.err
}

func (s *stubBackend) GetCallbackQueries(_ context.Context, _ *int, _ int, consume bool) ([]telegram.Map, error) {
	s.consumeCalls = append(s.consumeCalls, consume)
	return s.callbacks, s.err
}

func (s *stubBackend) AnswerCallbackQuery(_ context.Context, queryID string, _ string, _ bool) (bool, error) {
	s.answered = append(s.answered, queryID)
	return s.err == nil, s.err
}

func (s *stubBackend) RecordResumeSession(_ context.Context, _ any, messageID int, sessionID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.resumeStored[messageID] = sessionID
	return true, nil
}

func (s *stubBackend) LookupResumeSession(_ context.Context, _ any, messageID int) (string, error) {
	return s.resumeLookups[messageID], nil
}

func (s *stubBackend) ListChats(context.Context, int) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"id": 1, "title": "one"}}, nil
}

func (s *stubBackend) GetChatInfo(context.Context, any) (telegram.Map, error) {
	return telegram.Map{"id": 1}, s.err
}

func (s *stubBackend) CreateChat(_ context.Context, title string, _ bool) (telegram.Map, error) {
	return telegram.Map{"title": title}, s.err
}

func (s *stubBackend) JoinChat(context.Context, string) (bool, error) { return s.err == nil, s.err }
func (s *stubBackend) LeaveChat(context.Context, any) (bool, error)   { return s.err == nil, s.err }

func (s *stubBackend) GetMembers(context.Context, any, int) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"id": 7}}, nil
}

func (s *stubBackend) PromoteAdmin(context.Context, any, int64, bool) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) UpdateChatSettings(context.Context, any, *string, *string) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) ManageTopics(_ context.Context, _ any, action string, _ telegram.TopicOptions) (telegram.Map, error) {
	return telegram.Map{"action": action}, s.err
}

func (s *stubBackend) SendMedia(_ context.Context, _ any, mediaType, _ string, _ string) (telegram.Map, error) {
	return telegram.Map{"media_type": mediaType}, s.err
}

func (s *stubBackend) DownloadMedia(context.Context, any, int, string, string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "/tmp/file.jpg", nil
}

func (s *stubBackend) ListContacts(context.Context) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"id": 1}}, nil
}

func (s *stubBackend) SearchContacts(context.Context, string) ([]telegram.Map, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []telegram.Map{{"id": 2}}, nil
}

func (s *stubBackend) AddContact(context.Context, string, string, string) (bool, error) {
	return s.err == nil, s.err
}

func (s *stubBackend) BlockUser(context.Context, int64, bool) (bool, error) {
	return s.err == nil, s.err
}
