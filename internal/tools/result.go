// Package tools implements the action dispatch behind each MCP tool: it
// validates the arguments, calls the backend, and shapes the result.
package tools

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/security"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// Result is the JSON object a tool hands back.
type Result = map[string]any

// Ok returns a successful payload unchanged.
func Ok(data Result) Result {
	if data == nil {
		return Result{}
	}
	return data
}

// Err returns the payload shape every failed action uses. It is a result, not a
// protocol error: the model should read the message and retry with better
// arguments.
func Err(format string, args ...any) Result {
	return Result{"error": fmt.Sprintf(format, args...)}
}

// SafeError turns an error into a result, keeping the message only when it is
// one the server authored or already sanitised. Anything else is reported by
// type alone, so a failure deep in a dependency cannot leak internals.
func SafeError(err error) Result {
	var modeErr *telegram.ModeError
	var keyboardErr *telegram.KeyboardError
	var apiErr *telegram.APIError

	switch {
	case errors.As(err, &modeErr),
		errors.As(err, &keyboardErr),
		errors.As(err, &apiErr),
		security.IsSecurityError(err):
		return Err("%s", err.Error())
	}
	return Err("%T: Operation failed. Check server logs for details.", err)
}

// unknownAction is the message a mistyped action gets, with the nearest valid
// one suggested so the caller can fix it in one step.
func unknownAction(action string, valid []string) Result {
	sorted := append([]string(nil), valid...)
	sort.Strings(sorted)
	suggestion := ""
	if closest := closestMatch(action, sorted); closest != "" {
		suggestion = fmt.Sprintf(" Did you mean '%s'?", closest)
	}
	return Err("Unknown action '%s'.%s Valid: %s", action, suggestion, strings.Join(sorted, "|"))
}

// closestMatch returns the candidate nearest to value, or "" when none is close
// enough to be worth suggesting.
func closestMatch(value string, candidates []string) string {
	best := ""
	bestScore := 0.0
	for _, candidate := range candidates {
		score := similarity(strings.ToLower(value), strings.ToLower(candidate))
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	// 0.6 is difflib's own cutoff, which the Python server used.
	if bestScore < 0.6 {
		return ""
	}
	return best
}

// similarity is difflib's SequenceMatcher ratio: twice the number of matching
// characters over the combined length. Python's difflib is what the original
// server used to suggest a correction, and a plain edit distance is stricter --
// it would not offer "send" for "sned", which is exactly the typo worth
// catching.
func similarity(a, b string) float64 {
	left, right := []rune(a), []rune(b)
	total := len(left) + len(right)
	if total == 0 {
		return 1
	}
	return 2 * float64(matchingRunes(left, right)) / float64(total)
}

// matchingRunes counts the characters in the longest common substring plus,
// recursively, those in the parts to its left and right -- the same
// decomposition SequenceMatcher uses.
func matchingRunes(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	bestA, bestB, bestLen := 0, 0, 0
	// lengths[j] is the length of the common substring ending at a[i], b[j].
	lengths := make([]int, len(b)+1)
	for i := range a {
		next := make([]int, len(b)+1)
		for j := range b {
			if a[i] != b[j] {
				continue
			}
			next[j+1] = lengths[j] + 1
			if next[j+1] > bestLen {
				bestA, bestB, bestLen = i-next[j+1]+1, j-next[j+1]+1, next[j+1]
			}
		}
		lengths = next
	}
	if bestLen == 0 {
		return 0
	}
	return bestLen +
		matchingRunes(a[:bestA], b[:bestB]) +
		matchingRunes(a[bestA+bestLen:], b[bestB+bestLen:])
}
