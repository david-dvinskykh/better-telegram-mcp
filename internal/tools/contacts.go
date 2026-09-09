package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ContactArgs are the arguments of the `contact` tool.
type ContactArgs struct {
	Action    string `json:"action" jsonschema:"list|search|add|delete|block|import|export|blocked|last_seen|send_card|direct|alias_set|alias_list|alias_delete"`
	Query     string `json:"query,omitempty"`
	Phone     string `json:"phone,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Unblock   bool   `json:"unblock,omitempty"`
	Limit     int    `json:"limit,omitempty"`

	ChatID   any                     `json:"chat_id,omitempty" jsonschema:"chat id, @username or saved alias"`
	Contacts []telegram.ContactEntry `json:"contacts,omitempty" jsonschema:"rows of {phone, first_name, last_name} to import"`

	// Aliases: the local map from the words a person uses to a Telegram id.
	Alias   string `json:"alias,omitempty" jsonschema:"the free-text reference to remember, e.g. \"андрей бекендер\""`
	Replace bool   `json:"replace,omitempty" jsonschema:"repoint an alias that already names someone else"`
}

var contactActions = []string{
	"add", "alias_delete", "alias_list", "alias_set", "block", "blocked",
	"delete", "direct", "export", "import", "last_seen", "list", "search",
	"send_card",
}

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
		return handleContactExtra(ctx, backend, args)
	}
}

// handleContactExtra dispatches the address-book actions beyond the core four,
// plus the alias map, which is local and works in either mode.
func handleContactExtra(ctx context.Context, backend telegram.Backend, args ContactArgs) Result {
	if !contains(contactActions, args.Action) {
		return unknownAction(args.Action, contactActions)
	}
	// The aliases live in a local file, so they are reachable whichever backend
	// is connected -- including one that is not signed in yet.
	switch args.Action {
	case "alias_set", "alias_list", "alias_delete":
		return handleAlias(ctx, backend, args)
	}

	extras, err := telegram.Contacts(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "delete":
		if args.UserID == 0 {
			return Err("'delete' requires user_id")
		}
		deleted, err := extras.DeleteContact(ctx, args.UserID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"deleted": deleted})

	case "import":
		if len(args.Contacts) == 0 {
			return Err("'import' requires contacts, e.g. " +
				`[{"phone": "+491701234567", "first_name": "Ada"}]`)
		}
		imported, err := extras.ImportContacts(ctx, args.Contacts)
		if err != nil {
			return SafeError(err)
		}
		return Ok(imported)

	case "export":
		contacts, err := extras.ExportContacts(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"contacts": contacts, "count": len(contacts)})

	case "blocked":
		blocked, err := extras.ListBlocked(ctx, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"blocked": blocked, "count": len(blocked)})

	case "last_seen":
		if args.UserID == 0 {
			return Err("'last_seen' requires user_id")
		}
		last, err := extras.LastInteraction(ctx, args.UserID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(last)

	case "send_card":
		if isEmpty(args.ChatID) || args.UserID == 0 {
			return Err("'send_card' requires chat_id and user_id")
		}
		sent, err := extras.SendContactCard(ctx, args.ChatID, args.UserID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(sent)

	case "direct":
		if args.Query == "" {
			return Err("'direct' requires query")
		}
		chat, err := extras.DirectChat(ctx, args.Query)
		if err != nil {
			return SafeError(err)
		}
		return Ok(chat)

	default:
		return unknownAction(args.Action, contactActions)
	}
}
