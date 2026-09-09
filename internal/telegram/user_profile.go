package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/security"
)

// --- profile ---

// GetMe reports the account this session belongs to.
func (u *UserBackend) GetMe(ctx context.Context) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, err
	}
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			out := serializeUser(user)
			out["is_bot"] = user.Bot
			out["premium"] = user.Premium
			return out, nil
		}
	}
	return nil, errors.New("Telegram returned no account for this session.")
}

// UpdateProfile changes the fields a user can edit about themselves. A nil
// field is left as it is, which is what lets a caller set only the bio.
func (u *UserBackend) UpdateProfile(ctx context.Context, firstName, lastName, about *string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if firstName == nil && lastName == nil && about == nil {
		return nil, errors.New("'update' needs at least one of first_name, last_name or about")
	}
	request := &tg.AccountUpdateProfileRequest{}
	if firstName != nil {
		request.SetFirstName(*firstName)
	}
	if lastName != nil {
		request.SetLastName(*lastName)
	}
	if about != nil {
		request.SetAbout(*about)
	}
	user, err := api.AccountUpdateProfile(ctx, request)
	if err != nil {
		return nil, err
	}
	if typed, ok := user.(*tg.User); ok {
		return serializeUser(typed), nil
	}
	return Map{"updated": true}, nil
}

// SetProfilePhoto uploads a new avatar and makes it the current one.
func (u *UserBackend) SetProfilePhoto(ctx context.Context, path string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	file, _, err := u.uploadLocal(ctx, path)
	if err != nil {
		return nil, err
	}
	request := &tg.PhotosUploadProfilePhotoRequest{}
	request.SetFile(file)
	result, err := api.PhotosUploadProfilePhoto(ctx, request)
	if err != nil {
		return nil, err
	}
	if photo, ok := result.GetPhoto().(*tg.Photo); ok {
		return Map{"photo_id": photo.ID, "set": true}, nil
	}
	return Map{"set": true}, nil
}

// DeleteProfilePhoto removes the current avatar, leaving whatever was set
// before it as the account's photo.
func (u *UserBackend) DeleteProfilePhoto(ctx context.Context) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	photos, err := api.PhotosGetUserPhotos(ctx, &tg.PhotosGetUserPhotosRequest{
		UserID: &tg.InputUserSelf{},
		Limit:  1,
	})
	if err != nil {
		return false, err
	}
	current := photosOf(photos)
	if len(current) == 0 {
		return false, errors.New("This account has no profile photo to delete.")
	}
	photo, ok := current[0].(*tg.Photo)
	if !ok {
		return false, errors.New("This account has no profile photo to delete.")
	}
	deleted, err := api.PhotosDeletePhotos(ctx, []tg.InputPhotoClass{&tg.InputPhoto{
		ID:            photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
	}})
	if err != nil {
		return false, err
	}
	return len(deleted) > 0, nil
}

// GetUserPhotos lists the avatars of an account, most recent first.
func (u *UserBackend) GetUserPhotos(ctx context.Context, user any, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	target, err := u.inputUserOf(ctx, user)
	if err != nil {
		return nil, err
	}
	result, err := api.PhotosGetUserPhotos(ctx, &tg.PhotosGetUserPhotosRequest{
		UserID: target,
		Limit:  limitOr(limit, 20, 100),
	})
	if err != nil {
		return nil, err
	}
	out := []Map{}
	for _, item := range photosOf(result) {
		photo, ok := item.(*tg.Photo)
		if !ok {
			continue
		}
		_, hasVideo := photo.GetVideoSizes()
		out = append(out, Map{
			"photo_id": photo.ID,
			"date":     time.Unix(int64(photo.Date), 0).UTC().Format(time.RFC3339),
			"animated": hasVideo,
		})
	}
	return out, nil
}

// GetUserInfo returns the full profile of one account, which is where the bio,
// the common-chat count and a bot's description live. It answers both
// "who is this user" and "what is this bot".
func (u *UserBackend) GetUserInfo(ctx context.Context, user any) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	target, err := u.inputUserOf(ctx, user)
	if err != nil {
		return nil, err
	}
	full, err := api.UsersGetFullUser(ctx, target)
	if err != nil {
		return nil, err
	}

	u.rememberPeers(ctx, full.Users, full.Chats)

	out := Map{}
	for _, item := range full.Users {
		if typed, ok := item.(*tg.User); ok && typed.ID == full.FullUser.ID {
			out = serializeUser(typed)
			out["is_bot"] = typed.Bot
			out["premium"] = typed.Premium
			out["verified"] = typed.Verified
			out["status"] = userStatus(typed.Status)
			break
		}
	}
	out["id"] = full.FullUser.ID
	out["about"] = emptyToNil(full.FullUser.About)
	out["blocked"] = full.FullUser.Blocked
	out["common_chats"] = full.FullUser.CommonChatsCount
	if info, ok := full.FullUser.GetBotInfo(); ok {
		commands := []Map{}
		for _, command := range info.Commands {
			commands = append(commands, Map{
				"command":     command.Command,
				"description": command.Description,
			})
		}
		out["bot_info"] = Map{
			"description": emptyToNil(info.Description),
			"commands":    commands,
		}
	}
	return out, nil
}

// userStatus renders the last-seen state in the words Telegram shows, without
// inventing a timestamp the privacy settings may have withheld.
func userStatus(status tg.UserStatusClass) any {
	switch typed := status.(type) {
	case *tg.UserStatusOnline:
		return Map{"state": "online",
			"until": time.Unix(int64(typed.Expires), 0).UTC().Format(time.RFC3339)}
	case *tg.UserStatusOffline:
		return Map{"state": "offline",
			"last_seen": time.Unix(int64(typed.WasOnline), 0).UTC().Format(time.RFC3339)}
	case *tg.UserStatusRecently:
		return Map{"state": "recently"}
	case *tg.UserStatusLastWeek:
		return Map{"state": "last_week"}
	case *tg.UserStatusLastMonth:
		return Map{"state": "last_month"}
	case *tg.UserStatusEmpty:
		return Map{"state": "long_ago"}
	default:
		return nil
	}
}

// --- privacy ---

// privacyKeys maps the names a caller uses to the MTProto constructors. The
// names are the ones Telegram's own settings screen uses.
var privacyKeys = map[string]func() tg.InputPrivacyKeyClass{
	"status_timestamp": func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyStatusTimestamp{} },
	"phone_number":     func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyPhoneNumber{} },
	"phone_call":       func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyPhoneCall{} },
	"profile_photo":    func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyProfilePhoto{} },
	"forwards":         func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyForwards{} },
	"chat_invite":      func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyChatInvite{} },
	"added_by_phone":   func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyAddedByPhone{} },
	"voice_messages":   func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyVoiceMessages{} },
	"about":            func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyAbout{} },
	"birthday":         func() tg.InputPrivacyKeyClass { return &tg.InputPrivacyKeyBirthday{} },
}

// privacyRules are the whole-audience settings. Per-user exceptions exist in
// MTProto but are deliberately not exposed: naming individual accounts in a
// privacy rule is a change worth making in a Telegram client, where the person
// can see who they are allowing.
var privacyRules = map[string]func() tg.InputPrivacyRuleClass{
	"everybody": func() tg.InputPrivacyRuleClass { return &tg.InputPrivacyValueAllowAll{} },
	"contacts":  func() tg.InputPrivacyRuleClass { return &tg.InputPrivacyValueAllowContacts{} },
	"nobody":    func() tg.InputPrivacyRuleClass { return &tg.InputPrivacyValueDisallowAll{} },
}

func privacyKeyNames() []string {
	out := make([]string, 0, len(privacyKeys))
	for name := range privacyKeys {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func privacyRuleNames() []string {
	out := make([]string, 0, len(privacyRules))
	for name := range privacyRules {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// GetPrivacy reads one privacy setting.
func (u *UserBackend) GetPrivacy(ctx context.Context, key string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	build, ok := privacyKeys[key]
	if !ok {
		return nil, fmt.Errorf("Unknown privacy key %q. Valid: %s",
			key, strings.Join(privacyKeyNames(), "|"))
	}
	rules, err := api.AccountGetPrivacy(ctx, build())
	if err != nil {
		return nil, err
	}
	return Map{"key": key, "rules": describePrivacy(rules.GetRules())}, nil
}

// SetPrivacy replaces one privacy setting with a whole-audience rule.
func (u *UserBackend) SetPrivacy(ctx context.Context, key, rule string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	buildKey, ok := privacyKeys[key]
	if !ok {
		return nil, fmt.Errorf("Unknown privacy key %q. Valid: %s",
			key, strings.Join(privacyKeyNames(), "|"))
	}
	buildRule, ok := privacyRules[rule]
	if !ok {
		return nil, fmt.Errorf("Unknown privacy rule %q. Valid: %s",
			rule, strings.Join(privacyRuleNames(), "|"))
	}
	result, err := api.AccountSetPrivacy(ctx, &tg.AccountSetPrivacyRequest{
		Key:   buildKey(),
		Rules: []tg.InputPrivacyRuleClass{buildRule()},
	})
	if err != nil {
		return nil, err
	}
	return Map{"key": key, "rules": describePrivacy(result.GetRules())}, nil
}

// describePrivacy names the rules Telegram returns. The per-user forms report
// how many accounts they cover rather than listing them, which keeps a result
// readable when a person has hundreds of exceptions.
func describePrivacy(rules []tg.PrivacyRuleClass) []Map {
	out := []Map{}
	for _, rule := range rules {
		switch typed := rule.(type) {
		case *tg.PrivacyValueAllowAll:
			out = append(out, Map{"rule": "allow_everybody"})
		case *tg.PrivacyValueAllowContacts:
			out = append(out, Map{"rule": "allow_contacts"})
		case *tg.PrivacyValueAllowCloseFriends:
			out = append(out, Map{"rule": "allow_close_friends"})
		case *tg.PrivacyValueAllowPremium:
			out = append(out, Map{"rule": "allow_premium"})
		case *tg.PrivacyValueDisallowAll:
			out = append(out, Map{"rule": "disallow_everybody"})
		case *tg.PrivacyValueDisallowContacts:
			out = append(out, Map{"rule": "disallow_contacts"})
		case *tg.PrivacyValueAllowUsers:
			out = append(out, Map{"rule": "allow_users", "count": len(typed.Users)})
		case *tg.PrivacyValueDisallowUsers:
			out = append(out, Map{"rule": "disallow_users", "count": len(typed.Users)})
		case *tg.PrivacyValueAllowChatParticipants:
			out = append(out, Map{"rule": "allow_chats", "count": len(typed.Chats)})
		case *tg.PrivacyValueDisallowChatParticipants:
			out = append(out, Map{"rule": "disallow_chats", "count": len(typed.Chats)})
		}
	}
	return out
}

// --- shared helpers ---

// uploadLocal reads a local path or an http(s) URL through the security checks
// and uploads it, returning the handle and the name it was stored under.
func (u *UserBackend) uploadLocal(ctx context.Context, pathOrURL string) (tg.InputFileClass, string, error) {
	trimmed := strings.TrimSpace(pathOrURL)
	if trimmed == "" {
		return nil, "", errors.New("a file path or URL is required")
	}

	var (
		content []byte
		name    string
		err     error
	)
	if isHTTPURL(trimmed) {
		content, err = security.FetchURL(trimmed, 60*time.Second)
		if err != nil {
			return nil, "", err
		}
		name = filepath.Base(trimmed)
		if name == "" || name == "." || name == "/" {
			name = "file"
		}
	} else {
		path, pathErr := security.ValidateFilePath(trimmed)
		if pathErr != nil {
			return nil, "", pathErr
		}
		content, err = os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		name = filepath.Base(path)
	}

	file, err := u.uploader.FromBytes(ctx, name, content)
	if err != nil {
		return nil, "", err
	}
	return file, name, nil
}

// inputUserOf accepts the same references a chat_id does but insists the result
// is a person or a bot, which is what the profile calls need.
func (u *UserBackend) inputUserOf(ctx context.Context, user any) (tg.InputUserClass, error) {
	if user == nil {
		return &tg.InputUserSelf{}, nil
	}
	if text, ok := user.(string); ok {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "", "me", "self":
			return &tg.InputUserSelf{}, nil
		}
	}
	peer, err := u.resolvePeer(ctx, user)
	if err != nil {
		return nil, err
	}
	switch typed := peer.(type) {
	case *tg.InputPeerSelf:
		return &tg.InputUserSelf{}, nil
	case *tg.InputPeerUser:
		return &tg.InputUser{UserID: typed.UserID, AccessHash: typed.AccessHash}, nil
	default:
		return nil, errors.New("this action only applies to a user or a bot, not a group or channel")
	}
}

// limitOr applies the caller's limit, substituting a sane default for the zero
// value the MCP schema produces when the field is omitted.
func limitOr(limit, fallback, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	return clamp(limit, 1, max)
}

func photosOf(result tg.PhotosPhotosClass) []tg.PhotoClass {
	switch typed := result.(type) {
	case *tg.PhotosPhotos:
		return typed.Photos
	case *tg.PhotosPhotosSlice:
		return typed.Photos
	default:
		return nil
	}
}
