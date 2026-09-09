package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ProfileArgs are the arguments of the `profile` tool.
type ProfileArgs struct {
	Action string `json:"action" jsonschema:"me|update|photo_set|photo_delete|photos|user|privacy_get|privacy_set"`

	// User names the account to inspect; empty means the signed-in one.
	User any `json:"user,omitempty" jsonschema:"user id, @username, phone or saved alias"`

	FirstName *string `json:"first_name,omitempty"`
	LastName  *string `json:"last_name,omitempty"`
	About     *string `json:"about,omitempty"`

	Path string `json:"path,omitempty" jsonschema:"local file path or http(s) URL of the photo"`

	Key  string `json:"key,omitempty" jsonschema:"privacy setting, e.g. status_timestamp|phone_number|profile_photo"`
	Rule string `json:"rule,omitempty" jsonschema:"everybody|contacts|nobody"`

	Limit int `json:"limit,omitempty"`
}

var profileActions = []string{
	"me", "photo_delete", "photo_set", "photos", "privacy_get", "privacy_set", "update", "user",
}

// HandleProfile dispatches one call of the `profile` tool.
func HandleProfile(ctx context.Context, backend telegram.Backend, args ProfileArgs) Result {
	profile, err := telegram.Profile(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "me":
		account, err := profile.GetMe(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(account)

	case "update":
		if args.FirstName == nil && args.LastName == nil && args.About == nil {
			return Err("'update' requires at least one of first_name, last_name or about")
		}
		updated, err := profile.UpdateProfile(ctx, args.FirstName, args.LastName, args.About)
		if err != nil {
			return SafeError(err)
		}
		return Ok(updated)

	case "photo_set":
		if args.Path == "" {
			return Err("'photo_set' requires path")
		}
		set, err := profile.SetProfilePhoto(ctx, args.Path)
		if err != nil {
			return SafeError(err)
		}
		return Ok(set)

	case "photo_delete":
		deleted, err := profile.DeleteProfilePhoto(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"deleted": deleted})

	case "photos":
		photos, err := profile.GetUserPhotos(ctx, args.User, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"photos": photos, "count": len(photos)})

	case "user":
		info, err := profile.GetUserInfo(ctx, args.User)
		if err != nil {
			return SafeError(err)
		}
		return Ok(info)

	case "privacy_get":
		if args.Key == "" {
			return Err("'privacy_get' requires key")
		}
		setting, err := profile.GetPrivacy(ctx, args.Key)
		if err != nil {
			return SafeError(err)
		}
		return Ok(setting)

	case "privacy_set":
		if args.Key == "" || args.Rule == "" {
			return Err("'privacy_set' requires key and rule")
		}
		setting, err := profile.SetPrivacy(ctx, args.Key, args.Rule)
		if err != nil {
			return SafeError(err)
		}
		return Ok(setting)

	default:
		return unknownAction(args.Action, profileActions)
	}
}
