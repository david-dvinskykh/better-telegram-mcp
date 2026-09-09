package telegram

import (
	"fmt"
	"sort"
	"strings"
)

// Bot API inline-keyboard limits
// (https://core.telegram.org/bots/api#inlinekeyboardbutton). They are enforced
// here, before the request leaves the process, so a caller gets an actionable
// message instead of an opaque "Bad Request: BUTTON_DATA_INVALID".
const (
	MaxCallbackDataBytes = 64
	MaxButtonsPerRow     = 8
	MaxRows              = 100
	MaxButtons           = 100
	MaxButtonTextChars   = 64
)

// KeyboardError is a rejected buttons payload. It is a plain validation error:
// the message names the offending button by index.
type KeyboardError struct{ msg string }

func (e *KeyboardError) Error() string { return e.msg }

func keyboardErrorf(format string, args ...any) *KeyboardError {
	return &KeyboardError{msg: fmt.Sprintf(format, args...)}
}

// InlineButton is one callback button in the Bot API's own shape.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// InlineKeyboard is the reply_markup of a message carrying callback buttons.
type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

// BuildInlineKeyboard maps the compact `buttons` shape the MCP tools accept
// onto a Bot API InlineKeyboardMarkup.
//
// An empty payload yields an empty keyboard, which is how the Bot API removes
// the buttons from an existing message.
func BuildInlineKeyboard(buttons any) (*InlineKeyboard, error) {
	rows, err := normalizeRows(buttons)
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxRows {
		return nil, keyboardErrorf("buttons has %d rows; at most %d are allowed", len(rows), MaxRows)
	}

	keyboard := make([][]InlineButton, 0, len(rows))
	total := 0
	for rowIndex, row := range rows {
		if len(row) > MaxButtonsPerRow {
			return nil, keyboardErrorf(
				"buttons[%d] has %d buttons; at most %d fit in one row",
				rowIndex, len(row), MaxButtonsPerRow)
		}
		total += len(row)
		if total > MaxButtons {
			return nil, keyboardErrorf("buttons has more than %d buttons in total", MaxButtons)
		}
		built := make([]InlineButton, 0, len(row))
		for i, raw := range row {
			button, err := parseButton(rowIndex, i, raw)
			if err != nil {
				return nil, err
			}
			built = append(built, button)
		}
		keyboard = append(keyboard, built)
	}

	if dupes := duplicateData(keyboard); len(dupes) > 0 {
		return nil, keyboardErrorf(
			"duplicate callback data: %s. Each button needs distinct data so "+
				"presses can be told apart.", strings.Join(dupes, ", "))
	}
	return &InlineKeyboard{InlineKeyboard: keyboard}, nil
}

// normalizeRows accepts both the rows form [[btn, btn]] and the flat
// single-row form [btn, btn].
func normalizeRows(buttons any) ([][]any, error) {
	items, ok := buttons.([]any)
	if !ok {
		// A typed slice of rows (what the MCP argument decoder produces) is
		// the same shape once widened.
		if typed, ok := buttons.([][]map[string]string); ok {
			rows := make([][]any, 0, len(typed))
			for _, row := range typed {
				widened := make([]any, 0, len(row))
				for _, button := range row {
					widened = append(widened, button)
				}
				rows = append(rows, widened)
			}
			return rows, nil
		}
		return nil, keyboardErrorf(
			`buttons must be a list of rows, each row a list of ` +
				`{"text": ..., "data": ...} objects`)
	}

	if len(items) > 0 && allMaps(items) {
		return [][]any{items}, nil
	}
	rows := make([][]any, 0, len(items))
	for i, item := range items {
		row, ok := item.([]any)
		if !ok {
			return nil, keyboardErrorf(
				"buttons[%d] must be a list of buttons (a keyboard row), got %T", i, item)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func allMaps(items []any) bool {
	for _, item := range items {
		if !isMap(item) {
			return false
		}
	}
	return true
}

func isMap(item any) bool {
	switch item.(type) {
	case map[string]any, map[string]string:
		return true
	}
	return false
}

func parseButton(rowIndex, index int, raw any) (InlineButton, error) {
	where := fmt.Sprintf("buttons[%d][%d]", rowIndex, index)
	fields, err := asStringMap(raw)
	if err != nil {
		return InlineButton{}, keyboardErrorf(
			`%s must be an object like {"text": "Yes", "data": "DEC-12:yes"}`, where)
	}

	text := fields["text"]
	if strings.TrimSpace(text) == "" {
		return InlineButton{}, keyboardErrorf("%s.text is required and must be non-empty", where)
	}
	if len([]rune(text)) > MaxButtonTextChars {
		return InlineButton{}, keyboardErrorf("%s.text exceeds %d characters", where, MaxButtonTextChars)
	}

	// "callback_data" is the Bot API's own field name; accepting it as an alias
	// lets a caller paste a raw Bot API button without translating it.
	data, ok := fields["data"]
	if !ok || data == "" {
		data, ok = fields["callback_data"]
	}
	if !ok || data == "" {
		return InlineButton{}, keyboardErrorf(
			"%s.data is required and must be a non-empty string "+
				"(it is echoed back verbatim when the button is pressed)", where)
	}
	if size := len([]byte(data)); size > MaxCallbackDataBytes {
		return InlineButton{}, keyboardErrorf(
			"%s.data is %d bytes; the Bot API allows at most %d bytes",
			where, size, MaxCallbackDataBytes)
	}

	var unsupported []string
	for key := range fields {
		switch key {
		case "text", "data", "callback_data":
		default:
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return InlineButton{}, keyboardErrorf(
			"%s has unsupported field(s): %s. Only callback buttons (text + data) "+
				"are supported.", where, strings.Join(unsupported, ", "))
	}
	return InlineButton{Text: text, CallbackData: data}, nil
}

func asStringMap(raw any) (map[string]string, error) {
	switch value := raw.(type) {
	case map[string]string:
		return value, nil
	case map[string]any:
		out := make(map[string]string, len(value))
		for key, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("field %q is not a string", key)
			}
			out[key] = text
		}
		return out, nil
	default:
		return nil, fmt.Errorf("not an object")
	}
}

func duplicateData(keyboard [][]InlineButton) []string {
	seen := map[string]bool{}
	dupes := map[string]bool{}
	for _, row := range keyboard {
		for _, button := range row {
			if seen[button.CallbackData] {
				dupes[button.CallbackData] = true
			}
			seen[button.CallbackData] = true
		}
	}
	out := make([]string, 0, len(dupes))
	for data := range dupes {
		out = append(out, data)
	}
	sort.Strings(out)
	return out
}
