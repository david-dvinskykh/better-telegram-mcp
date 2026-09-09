package tools

import (
	"context"
	"errors"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/aliases"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// handleAlias edits the local map from the words a person uses for someone to
// the Telegram id behind them.
//
// This is the loop that makes every other tool usable in conversation: when a
// reference like "андрею бекендеру" cannot be resolved, the resolver returns an
// instruction to ask who that is; the answer is saved here, and from then on
// the same wording resolves without asking again.
func handleAlias(ctx context.Context, backend telegram.Backend, args ContactArgs) Result {
	store := telegram.AliasesOf(backend)

	switch args.Action {
	case "alias_list":
		all := store.List()
		out := make([]Result, 0, len(all))
		for alias, entry := range all {
			out = append(out, Result{"alias": alias, "id": entry.ID, "name": entry.Name})
		}
		return Ok(Result{"aliases": out, "count": len(out), "file": store.Path()})

	case "alias_delete":
		if args.Alias == "" {
			return Err("'alias_delete' requires alias")
		}
		deleted, err := store.Delete(args.Alias)
		if err != nil {
			return aliasError(err)
		}
		if !deleted {
			return Err("No alias %q is saved.", args.Alias)
		}
		return Ok(Result{"deleted": true, "alias": args.Alias})

	case "alias_set":
		if args.Alias == "" || isEmpty(args.ChatID) {
			return Err("'alias_set' requires alias and chat_id -- the wording the " +
				"person actually used, and who it points at")
		}
		// The id is resolved through Telegram first, so an alias can never be
		// saved pointing at something that does not exist.
		info, err := backend.GetChatInfo(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		id, ok := asInt64(info["id"])
		if !ok {
			return Err("Telegram did not return an id for that chat, so the alias was not saved.")
		}
		name, _ := info["title"].(string)
		if name == "" {
			first, _ := info["first_name"].(string)
			last, _ := info["last_name"].(string)
			name = trimSpaceJoin(first, last)
		}

		saved, err := store.Save(args.Alias, aliases.Entry{ID: id, Name: name}, args.Replace)
		if err != nil {
			var existing *aliases.Error
			if errors.As(err, &existing) {
				return Err("%s", existing.Message)
			}
			return SafeError(err)
		}
		return Ok(Result{
			"saved": true,
			"alias": aliases.Key(args.Alias),
			"id":    saved.ID,
			"name":  saved.Name,
		})

	default:
		return unknownAction(args.Action, contactActions)
	}
}

// aliasError passes an alias-store message through, because the store writes
// them for the caller to read rather than about its own internals.
func aliasError(err error) Result {
	var aliasErr *aliases.Error
	if errors.As(err, &aliasErr) {
		return Err("%s", aliasErr.Message)
	}
	return SafeError(err)
}

func trimSpaceJoin(parts ...string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out != "" {
			out += " "
		}
		out += part
	}
	return out
}
