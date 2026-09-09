package tools

import (
	"encoding/json"
	"strconv"
	"strings"
)

// isEmpty reports whether a chat_id-shaped argument was actually supplied.
// The MCP wire type is open (a username or a numeric id), so "unset" has to be
// recognised across both.
func isEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case float64:
		return typed == 0
	default:
		return false
	}
}

func asInt(value any) (int, bool) {
	parsed, ok := asInt64(value)
	return int(parsed), ok
}

func asInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed, true
		}
	case string:
		if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
