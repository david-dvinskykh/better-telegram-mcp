package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

func errorText(t *testing.T, result Result) string {
	t.Helper()
	message, ok := result["error"].(string)
	if !ok {
		t.Fatalf("expected an error result, got %#v", result)
	}
	return message
}

func TestSendRequiresChatAndText(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{Action: "send"})
	if !strings.Contains(errorText(t, result), "requires chat_id and text") {
		t.Errorf("the message should name what is missing: %s", errorText(t, result))
	}
}

func TestSendPassesOptionsThrough(t *testing.T) {
	stub := newStub()
	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action:    "send",
		ChatID:    "@david",
		Text:      "hi",
		ReplyTo:   11,
		ParseMode: "HTML",
	})
	if _, isError := result["error"]; isError {
		t.Fatalf("unexpected error: %#v", result)
	}
	if stub.sentChat != "@david" || stub.sentText != "hi" {
		t.Errorf("wrong target or text: %v / %q", stub.sentChat, stub.sentText)
	}
	if stub.sentOptions.ReplyTo != 11 || stub.sentOptions.ParseMode != "HTML" {
		t.Errorf("options were dropped: %#v", stub.sentOptions)
	}
}

// A send with no buttons must look exactly like one from a caller that has
// never heard of them.
func TestSendWithoutButtonsSendsNoKeyboard(t *testing.T) {
	stub := newStub()
	HandleMessage(context.Background(), stub, MessageArgs{
		Action: "send", ChatID: 1, Text: "hi",
	})
	if stub.sentOptions.Buttons != nil {
		t.Errorf("expected no keyboard, got %#v", stub.sentOptions.Buttons)
	}
}

func TestSendRegistersTheResumeSession(t *testing.T) {
	stub := newStub()
	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action:        "send",
		ChatID:        -100123,
		Text:          "Approve?",
		Buttons:       []any{map[string]any{"text": "Yes", "data": "y"}},
		ResumeSession: "session_01AbCdEfGhIjKl",
	})
	if result["resume_registered"] != true {
		t.Fatalf("expected the registration to be reported, got %#v", result)
	}
	if stub.resumeStored[42] != "session_01AbCdEfGhIjKl" {
		t.Errorf("the session was not tied to the sent message: %#v", stub.resumeStored)
	}
}

func TestEditRequiresTextOrButtons(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "edit", ChatID: 1, MessageID: 2,
	})
	if !strings.Contains(errorText(t, result), "text and/or buttons") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

// Editing with buttons only is how a keyboard is replaced or stripped, and it
// must not go through the text edit path.
func TestEditWithButtonsOnlyReplacesTheKeyboard(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "edit", ChatID: 1, MessageID: 2, Buttons: []any{},
	})
	if result["buttons_replaced"] != true {
		t.Errorf("expected the keyboard-only edit path, got %#v", result)
	}
}

func TestUnknownActionSuggestsTheNearestOne(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{Action: "sned"})
	message := errorText(t, result)
	if !strings.Contains(message, "Did you mean 'send'?") {
		t.Errorf("expected a suggestion, got %s", message)
	}
	if !strings.Contains(message, "Valid: answer|callbacks") {
		t.Errorf("the valid actions should be listed, got %s", message)
	}
}

func TestHistoryReportsACount(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "history", ChatID: 1,
	})
	if result["count"] != 2 {
		t.Errorf("expected a count alongside the messages, got %#v", result)
	}
}

func callback(updateID, messageID int, data string, fromID int64) telegram.Map {
	return telegram.Map{
		"update_id": updateID,
		"callback_query": map[string]any{
			"id":   "q" + data,
			"data": data,
			"from": map[string]any{"id": float64(fromID), "username": "presser"},
			"message": map[string]any{
				"message_id": float64(messageID),
				"date":       float64(1700000000),
				"chat":       map[string]any{"id": float64(-100123)},
			},
		},
	}
}

func TestCallbacksAcknowledgesAndFlattensAPress(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{callback(7, 5, "DEC-1:yes", 99)}
	stub.resumeLookups[5] = "session_01AbCdEfGhIjKl"

	result := HandleMessage(context.Background(), stub, MessageArgs{Action: "callbacks"})
	presses, ok := result["callbacks"].([]Result)
	if !ok || len(presses) != 1 {
		t.Fatalf("expected one press, got %#v", result["callbacks"])
	}

	press := presses[0]
	if press["data"] != "DEC-1:yes" || press["from_username"] != "presser" {
		t.Errorf("the press was not flattened correctly: %#v", press)
	}
	if press["session_id"] != "session_01AbCdEfGhIjKl" {
		t.Errorf("the asking session should come back with the press: %#v", press)
	}
	if press["answered"] != true {
		t.Error("an unanswered press leaves a spinner and must be acknowledged")
	}
	if len(stub.answered) != 1 || stub.answered[0] != "qDEC-1:yes" {
		t.Errorf("expected exactly one acknowledgement, got %v", stub.answered)
	}
	if result["cursor"] != 7 {
		t.Errorf("the cursor should advance to the press, got %#v", result["cursor"])
	}
}

func TestCallbacksPeekTakesNothing(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{callback(7, 5, "DEC-1:yes", 99)}

	result := HandleMessage(context.Background(), stub, MessageArgs{Action: "callbacks", Peek: true})
	if result["peek"] != true {
		t.Errorf("a peek should say so in the result: %#v", result)
	}
	if len(stub.answered) != 0 {
		t.Errorf("a peek must not answer anything, got %v", stub.answered)
	}
	if len(stub.consumeCalls) != 1 || stub.consumeCalls[0] {
		t.Errorf("a peek must read without consuming, got %v", stub.consumeCalls)
	}
}

func TestCallbacksAlreadyAnsweredPressIsNotAnsweredAgain(t *testing.T) {
	stub := newStub()
	update := callback(7, 5, "DEC-1:yes", 99)
	update["answered"] = true
	stub.callbacks = []telegram.Map{update}

	HandleMessage(context.Background(), stub, MessageArgs{Action: "callbacks"})
	if len(stub.answered) != 0 {
		t.Errorf("a query id spent by the queue writer must not be reused, got %v", stub.answered)
	}
}

func TestCallbacksRejectsUnauthorizedSender(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{callback(7, 5, "DEC-1:yes", 99)}

	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action:         "callbacks",
		AllowedFromIDs: []int64{1234},
	})
	if result["count"] != 0 {
		t.Errorf("a press from an unlisted user must not be accepted: %#v", result)
	}
	ignored := result["ignored"].([]Result)
	if len(ignored) != 1 || ignored[0]["reason"] != "unauthorized_sender" {
		t.Errorf("expected an unauthorized_sender rejection, got %#v", ignored)
	}
}

func TestCallbacksRequiresAFullDataPatternMatch(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{callback(7, 5, "DEC-1:yes-and-more", 99)}

	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action:      "callbacks",
		DataPattern: `DEC-\d+:(yes|no)`,
	})
	ignored := result["ignored"].([]Result)
	if len(ignored) != 1 || ignored[0]["reason"] != "data_pattern_mismatch" {
		t.Errorf("a partial match must not pass the filter, got %#v", ignored)
	}
}

func TestCallbacksAcceptsAFullDataPatternMatch(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{callback(7, 5, "DEC-1:yes", 99)}

	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action:      "callbacks",
		DataPattern: `DEC-\d+:(yes|no)`,
	})
	if result["count"] != 1 {
		t.Errorf("expected the matching press to be accepted, got %#v", result)
	}
}

func TestCallbacksRejectsInvalidPattern(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "callbacks", DataPattern: "(unclosed",
	})
	if !strings.Contains(errorText(t, result), "Invalid data_pattern regex") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

// Narrowing to one question while consuming would silently drop every other
// press, so the combination is refused rather than quietly losing decisions.
func TestCallbacksMessageIDRequiresPeek(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "callbacks", MessageID: 5,
	})
	if !strings.Contains(errorText(t, result), "peek=true") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

func TestCallbacksMessageIDFiltersToOneQuestion(t *testing.T) {
	stub := newStub()
	stub.callbacks = []telegram.Map{
		callback(7, 5, "a", 99),
		callback(8, 6, "b", 99),
	}
	result := HandleMessage(context.Background(), stub, MessageArgs{
		Action: "callbacks", MessageID: 6, Peek: true,
	})
	presses := result["callbacks"].([]Result)
	if len(presses) != 1 || presses[0]["message_id"] != int64(6) {
		t.Errorf("expected only the watched question's press, got %#v", presses)
	}
}

func TestAnswerRequiresAQueryID(t *testing.T) {
	result := HandleMessage(context.Background(), newStub(), MessageArgs{Action: "answer"})
	if !strings.Contains(errorText(t, result), "requires callback_query_id") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

func TestAnswerTextIsLengthChecked(t *testing.T) {
	long := strings.Repeat("x", MaxAnswerText+1)
	result := HandleMessage(context.Background(), newStub(), MessageArgs{
		Action: "answer", CallbackQueryID: "q1", AnswerText: long,
	})
	if !strings.Contains(errorText(t, result), "answer_text exceeds 200 characters") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

// A user-mode backend refusing a bot-only action should say which mode is
// needed, not fail opaquely.
func TestModeErrorsSurfaceTheirMessage(t *testing.T) {
	stub := newStub()
	stub.err = telegram.NeedsBot()

	result := HandleMessage(context.Background(), stub, MessageArgs{Action: "answer", CallbackQueryID: "q"})
	if !strings.Contains(errorText(t, result), "requires bot mode") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}
