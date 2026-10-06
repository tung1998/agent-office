package api

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Avatar colors the dashboard knows (Tailwind palette names).
var avatarColors = []string{"sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink", "rose", "red", "orange", "amber", "yellow", "lime", "green", "emerald", "teal", "cyan", "slate"}

var (
	avatarIcon  = regexp.MustCompile(`^i-lucide-[a-z0-9-]{1,40}$`)
	avatarImage = regexp.MustCompile(`^data:image/(png|jpeg|webp|gif);base64,[A-Za-z0-9+/=]+$`)
)

const maxAvatarImage = 200 << 10 // a small picture, stored with the agent

// checkAvatar: a known color, a lucide icon, and only a small inline image
// (never a remote URL: the dashboard shows it to everyone).
func checkAvatar(a *storage.Avatar) error {
	if a == nil {
		return nil
	}
	if a.Color != "" && !slices.Contains(avatarColors, a.Color) {
		return errors.New("màu avatar không hợp lệ")
	}
	if a.Icon != "" && !avatarIcon.MatchString(a.Icon) {
		return errors.New("icon avatar không hợp lệ")
	}
	if a.Image != "" {
		if len(a.Image) > maxAvatarImage {
			return errors.New("ảnh avatar quá lớn (tối đa 200KB)")
		}
		if !avatarImage.MatchString(strings.TrimSpace(a.Image)) {
			return errors.New("ảnh avatar phải là ảnh PNG, JPEG, WebP hoặc GIF")
		}
	}
	return nil
}
