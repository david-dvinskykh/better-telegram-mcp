package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ContactArgs are the arguments of the `contact` tool.
type ContactArgs struct {
	Action    string `json:"action" jsonschema:"list|search|add|block"`
	Query     string `json:"query,omitempty"`
	Phone     string `json:"phone,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Unblock   bool   `json:"unblock,omitempty"`
}

var contactActions = []string{"add", "block", "list", "search"}

// HandleContact dispatches one call of the `contact` tool.
func HandleContact(ctx context.Context, backend telegram.Backend, args ContactArgs) Result {
	switch args.Action {
	case "list":
		contacts, err := backend.ListContacts(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"contacts": contacts, "count": len(contacts)})

	case "search":
		if args.Query == "" {
			return Err("'search' requires query")
		}
		contacts, err := backend.SearchContacts(ctx, args.Query)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"contacts": contacts, "count": len(contacts)})

	case "add":
		if args.Phone == "" || args.FirstName == "" {
			return Err("'add' requires phone and first_name")
		}
		added, err := backend.AddContact(ctx, args.Phone, args.FirstName, args.LastName)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"added": added})

	case "block":
		if args.UserID == 0 {
			return Err("'block' requires user_id")
		}
		changed, err := backend.BlockUser(ctx, args.UserID, args.Unblock)
		if err != nil {
			return SafeError(err)
		}
		word := "blocked"
		if args.Unblock {
			word = "unblocked"
		}
		return Ok(Result{word: changed})

	default:
		return unknownAction(args.Action, contactActions)
	}
}
