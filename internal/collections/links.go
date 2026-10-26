package collections

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Links (ADR-218): every project keeps the links the person wants to find
// again (a doc, a download a build made, a site) in its collection "links",
// shown on its Thông tin tab; agents save them with data_upsert, and the
// review after a turn saves the ones the turn made or was given.
const LinksKey = "links"

// LinkKinds are a link's kinds; the last is the default.
var LinkKinds = []string{"tai-lieu", "tai-ve", "web", "khac"}

func linksCollection() storage.Collection {
	return storage.Collection{Key: LinksKey, Name: "Liên kết",
		Description: "Link người dùng muốn giữ để tìm lại: tài liệu, link tải (bản build), trang web. Một link một bản ghi (khớp theo url).",
		Fields: []storage.Field{
			{Name: "title", Label: "Tên", Type: storage.FieldText, Required: true},
			{Name: "url", Label: "Link", Type: storage.FieldText, Required: true, Unique: true}, // a URL or a path on the machine
			{Name: "kind", Label: "Loại", Type: storage.FieldEnum, Options: LinkKinds, Indexed: true},
			{Name: "note", Label: "Ghi chú", Type: storage.FieldText},
		}}
}

// ensureLinks makes the project's links collection when it has none.
func (s *Service) ensureLinks(ctx context.Context, projectID string) error {
	if projectID == "" {
		return nil
	}
	if _, err := s.store.Collections().Get(ctx, projectID, LinksKey); !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	_, err := s.Define(ctx, projectID, linksCollection())
	return err
}

// Link is one link to keep.
type Link struct{ Title, URL, Kind, Note string }

// SaveLink keeps l in the project's links (the one with the same url is
// updated); by is who saved it.
func (s *Service) SaveLink(ctx context.Context, projectID string, l Link, by string) error {
	l.Title, l.URL, l.Note = strings.TrimSpace(l.Title), strings.TrimSpace(l.URL), strings.TrimSpace(l.Note)
	if l.URL == "" {
		return invalid("link trống")
	}
	if !slices.Contains(LinkKinds, l.Kind) {
		l.Kind = LinkKinds[len(LinkKinds)-1]
	}
	data := map[string]any{"title": l.Title, "url": l.URL, "kind": l.Kind}
	if l.Title == "" { // a link kept already keeps its title
		c, err := s.Collection(ctx, projectID, LinksKey)
		if err != nil {
			return err
		}
		if _, err := s.store.Collections().FindBy(ctx, c.ID, "url", l.URL); err == nil {
			delete(data, "title")
		} else {
			data["title"] = l.URL
		}
	}
	if l.Note != "" {
		data["note"] = l.Note
	}
	_, _, err := s.Upsert(ctx, projectID, LinksKey, "", data, by)
	return err
}
