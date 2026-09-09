package telegram

import (
	"strings"
	"testing"
)

func TestBuildInlineKeyboardRows(t *testing.T) {
	keyboard, err := BuildInlineKeyboard([]any{
		[]any{
			map[string]any{"text": "Yes", "data": "DEC-12:yes"},
			map[string]any{"text": "No", "data": "DEC-12:no"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keyboard.InlineKeyboard) != 1 || len(keyboard.InlineKeyboard[0]) != 2 {
		t.Fatalf("expected one row of two buttons, got %#v", keyboard.InlineKeyboard)
	}
	if keyboard.InlineKeyboard[0][0].CallbackData != "DEC-12:yes" {
		t.Errorf("callback data not carried through: %#v", keyboard.InlineKeyboard[0][0])
	}
}

func TestBuildInlineKeyboardAcceptsFlatSingleRow(t *testing.T) {
	keyboard, err := BuildInlineKeyboard([]any{
		map[string]any{"text": "Yes", "data": "yes"},
		map[string]any{"text": "No", "data": "no"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keyboard.InlineKeyboard) != 1 {
		t.Fatalf("a flat list should become one row, got %d", len(keyboard.InlineKeyboard))
	}
}

func TestBuildInlineKeyboardEmptyStripsKeyboard(t *testing.T) {
	keyboard, err := BuildInlineKeyboard([]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keyboard.InlineKeyboard) != 0 {
		t.Errorf("an empty payload must produce an empty keyboard, got %#v", keyboard)
	}
}

func TestBuildInlineKeyboardAcceptsCallbackDataAlias(t *testing.T) {
	keyboard, err := BuildInlineKeyboard([]any{
		map[string]any{"text": "Yes", "callback_data": "yes"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keyboard.InlineKeyboard[0][0].CallbackData != "yes" {
		t.Errorf("callback_data alias not accepted: %#v", keyboard.InlineKeyboard[0][0])
	}
}

func TestBuildInlineKeyboardRejects(t *testing.T) {
	tests := []struct {
		name    string
		buttons any
		wants   string
	}{
		{
			name:    "missing text",
			buttons: []any{map[string]any{"data": "x"}},
			wants:   "text is required",
		},
		{
			name:    "missing data",
			buttons: []any{map[string]any{"text": "Yes"}},
			wants:   "data is required",
		},
		{
			name: "oversize callback data",
			buttons: []any{map[string]any{
				"text": "Yes", "data": strings.Repeat("x", MaxCallbackDataBytes+1),
			}},
			wants: "the Bot API allows at most 64 bytes",
		},
		{
			name:    "unsupported field",
			buttons: []any{map[string]any{"text": "Yes", "data": "y", "url": "https://x"}},
			wants:   "unsupported field(s): url",
		},
		{
			name: "duplicate data",
			buttons: []any{
				map[string]any{"text": "Yes", "data": "same"},
				map[string]any{"text": "No", "data": "same"},
			},
			wants: "duplicate callback data: same",
		},
		{
			name:    "not a list",
			buttons: "yes",
			wants:   "buttons must be a list of rows",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildInlineKeyboard(test.buttons)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", test.wants)
			}
			if !strings.Contains(err.Error(), test.wants) {
				t.Errorf("error %q does not mention %q", err, test.wants)
			}
		})
	}
}

func TestBuildInlineKeyboardRejectsOversizeRow(t *testing.T) {
	row := make([]any, MaxButtonsPerRow+1)
	for i := range row {
		row[i] = map[string]any{"text": "b", "data": string(rune('a' + i))}
	}
	_, err := BuildInlineKeyboard([]any{row})
	if err == nil || !strings.Contains(err.Error(), "fit in one row") {
		t.Fatalf("expected a per-row limit error, got %v", err)
	}
}

// A button whose text is exactly at the limit is legal; one rune more is not.
func TestBuildInlineKeyboardTextLimitIsInclusive(t *testing.T) {
	atLimit := strings.Repeat("t", MaxButtonTextChars)
	if _, err := BuildInlineKeyboard([]any{
		map[string]any{"text": atLimit, "data": "x"},
	}); err != nil {
		t.Fatalf("text of exactly %d chars must be accepted: %v", MaxButtonTextChars, err)
	}
	if _, err := BuildInlineKeyboard([]any{
		map[string]any{"text": atLimit + "t", "data": "x"},
	}); err == nil {
		t.Fatalf("text of %d chars must be rejected", MaxButtonTextChars+1)
	}
}
