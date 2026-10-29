// Package collections keeps a project's structured data in office's database
// without code (ADR-172): a collection is a typed schema (fields, which are
// required, unique, indexed), its records JSON checked against it. Every
// change keeps what the record was. A feature pack declares its own
// collections (support: customers, playbooks); agents read and write them
// with the data_* tools, scripts and the dashboard through the API.
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Limits.
const (
	MaxFields   = 50
	MaxData     = 64 << 10 // bytes of one record's JSON
	MaxRecords  = 50000    // records of one project
	MaxPageSize = 200
)

// InvalidError is a request that cannot be done as asked (bad schema, data,
// filter): the caller shows its text.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return &InvalidError{fmt.Sprintf(format, a...)} }

// IsInvalid reports a request error (vs. a failure).
func IsInvalid(err error) bool {
	var e *InvalidError
	return errors.As(err, &e)
}

// Service reads and writes collections.
type Service struct {
	store      storage.Store
	maxRecords int
}

// New builds the service.
func New(store storage.Store) *Service { return &Service{store: store, maxRecords: MaxRecords} }

var name = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

var types = []string{storage.FieldText, storage.FieldNumber, storage.FieldBool, storage.FieldDate, storage.FieldEnum,
	storage.FieldTags, storage.FieldRef, storage.FieldConversation, storage.FieldURL, storage.FieldFile}

// Define creates a collection or changes its name, description and fields
// (a field taken out keeps its values in the records, unread).
func (s *Service) Define(ctx context.Context, projectID string, c storage.Collection) (storage.Collection, error) {
	c.ProjectID, c.Key, c.Name = projectID, strings.TrimSpace(c.Key), strings.TrimSpace(c.Name)
	if !name.MatchString(c.Key) {
		return c, invalid("key %q: chữ thường, số, gạch dưới, bắt đầu bằng chữ, tối đa 40 ký tự", c.Key)
	}
	if c.Name == "" {
		c.Name = c.Key
	}
	if len(c.Fields) == 0 || len(c.Fields) > MaxFields {
		return c, invalid("collection cần 1 đến %d trường", MaxFields)
	}
	seen := map[string]bool{}
	for i := range c.Fields {
		f := &c.Fields[i]
		f.Name, f.Type = strings.TrimSpace(f.Name), strings.TrimSpace(f.Type)
		switch {
		case !name.MatchString(f.Name) || f.Name == "id":
			return c, invalid("trường %q: tên chữ thường, số, gạch dưới (không dùng id)", f.Name)
		case seen[f.Name]:
			return c, invalid("trường %q trùng tên", f.Name)
		case !slices.Contains(types, f.Type):
			return c, invalid("trường %q: kiểu %q không có (%s)", f.Name, f.Type, strings.Join(types, ", "))
		case f.Type == storage.FieldEnum && len(f.Options) == 0:
			return c, invalid("trường %q: enum cần danh sách lựa chọn", f.Name)
		case f.Type == storage.FieldRef && !name.MatchString(f.Ref):
			return c, invalid("trường %q: ref cần key của collection được trỏ tới", f.Name)
		case f.Unique && (f.Type == storage.FieldTags || f.Type == storage.FieldBool):
			return c, invalid("trường %q: kiểu %s không đặt unique được", f.Name, f.Type)
		}
		seen[f.Name] = true
		if f.Type != storage.FieldEnum {
			f.Options = nil
		}
		if f.Type != storage.FieldRef {
			f.Ref = ""
		}
	}
	for _, f := range c.Fields {
		if f.Type == storage.FieldRef && f.Ref != c.Key {
			if _, err := s.store.Collections().Get(ctx, projectID, f.Ref); err != nil {
				return c, invalid("trường %q trỏ tới collection %q chưa có", f.Name, f.Ref)
			}
		}
	}
	var out storage.Collection
	err := s.store.InTx(ctx, func(tx storage.Store) error {
		old, err := tx.Collections().Get(ctx, projectID, c.Key)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			if out, err = tx.Collections().Create(ctx, c); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			old.Name, old.Description, old.Fields = c.Name, c.Description, c.Fields
			if c.Pack != "" {
				old.Pack = c.Pack
			}
			if err := tx.Collections().Update(ctx, old); err != nil {
				return err
			}
			out = old
		}
		if err := tx.Collections().Index(ctx, out); errors.Is(err, storage.ErrConflict) {
			return invalid("không đặt unique được: đã có bản ghi trùng giá trị")
		} else if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return storage.Collection{}, err
	}
	return s.store.Collections().Get(ctx, projectID, c.Key)
}

// List lists a project's collections (its links always among them).
func (s *Service) List(ctx context.Context, projectID string) ([]storage.Collection, error) {
	if err := s.ensureLinks(ctx, projectID); err != nil {
		return nil, err
	}
	if err := s.ensureFiles(ctx, projectID); err != nil {
		return nil, err
	}
	return s.store.Collections().List(ctx, projectID)
}

// Collection reads one collection by key.
func (s *Service) Collection(ctx context.Context, projectID, key string) (storage.Collection, error) {
	if key == LinksKey || key == FilesKey {
		ensure := s.ensureLinks
		if key == FilesKey {
			ensure = s.ensureFiles
		}
		if err := ensure(ctx, projectID); err != nil {
			return storage.Collection{}, err
		}
	}
	c, err := s.store.Collections().Get(ctx, projectID, key)
	if errors.Is(err, storage.ErrNotFound) {
		return c, invalid("không có collection %q", key)
	}
	return c, err
}

// Upsert writes a record: the one id names (its fields patched), else the
// one a unique field's value matches, else a new one. A field set to null is
// taken out. It reports whether the record is new.
func (s *Service) Upsert(ctx context.Context, projectID, key, id string, data map[string]any, by string) (storage.Record, bool, error) {
	c, err := s.Collection(ctx, projectID, key)
	if err != nil {
		return storage.Record{}, false, err
	}
	patch, err := s.normalize(ctx, c, data)
	if err != nil {
		return storage.Record{}, false, err
	}
	var (
		out     storage.Record
		created bool
	)
	err = s.store.InTx(ctx, func(tx storage.Store) error {
		repo := tx.Collections()
		var old storage.Record
		found := false
		if id != "" {
			if old, err = repo.Record(ctx, c.ID, id); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					return invalid("không có bản ghi %q trong %q", id, key)
				}
				return err
			}
			found = true
		} else {
			for _, f := range c.Fields {
				if v, ok := patch[f.Name]; f.Unique && ok && v != nil {
					if old, err = repo.FindBy(ctx, c.ID, f.Name, sqlValue(v)); err == nil {
						found = true
						break
					} else if !errors.Is(err, storage.ErrNotFound) {
						return err
					}
				}
			}
		}
		next := map[string]any{}
		if found {
			for k, v := range old.Data {
				next[k] = v
			}
		}
		for k, v := range patch {
			if v == nil {
				delete(next, k)
			} else {
				next[k] = v
			}
		}
		if err := checkRecord(c, next); err != nil {
			return err
		}
		x := storage.Record{CollectionID: c.ID, ProjectID: projectID, Data: next, Text: text(c, next), CreatedBy: by, UpdatedBy: by}
		if found {
			x.ID, x.CreatedBy, x.CreatedAt = old.ID, old.CreatedBy, old.CreatedAt
			if err := repo.Save(ctx, x); err != nil {
				return conflict(err)
			}
			out = x
			return nil
		}
		if n, err := repo.Count(ctx, projectID); err != nil {
			return err
		} else if n >= s.maxRecords {
			return invalid("project đã có %d bản ghi, tối đa %d", n, s.maxRecords)
		}
		if out, err = repo.Insert(ctx, x); err != nil {
			return conflict(err)
		}
		created = true
		return nil
	})
	if err != nil {
		return storage.Record{}, false, err
	}
	out, err = s.store.Collections().Record(ctx, c.ID, out.ID)
	return out, created, err
}

func conflict(err error) error {
	if errors.Is(err, storage.ErrConflict) {
		return invalid("giá trị của trường unique đã có ở bản ghi khác")
	}
	return err
}

// Get reads a record and the records its ref fields point to (by field).
func (s *Service) Get(ctx context.Context, projectID, key, id string) (storage.Record, map[string]storage.Record, error) {
	c, err := s.Collection(ctx, projectID, key)
	if err != nil {
		return storage.Record{}, nil, err
	}
	x, err := s.store.Collections().Record(ctx, c.ID, id)
	if errors.Is(err, storage.ErrNotFound) {
		return x, nil, invalid("không có bản ghi %q trong %q", id, key)
	} else if err != nil {
		return x, nil, err
	}
	refs := map[string]storage.Record{}
	for _, f := range c.Fields {
		v, _ := x.Data[f.Name].(string)
		if f.Type != storage.FieldRef || v == "" {
			continue
		}
		if rc, err := s.store.Collections().Get(ctx, projectID, f.Ref); err == nil {
			if r, err := s.store.Collections().Record(ctx, rc.ID, v); err == nil {
				refs[f.Name] = r
			}
		}
	}
	return x, refs, nil
}

// Delete takes a record out (its history keeps what it was).
func (s *Service) Delete(ctx context.Context, projectID, key, id, by string) error {
	x, _, err := s.Get(ctx, projectID, key, id)
	if err != nil {
		return err
	}
	return s.store.Collections().Delete(ctx, x, by)
}

// History lists a record's changes, the latest first.
func (s *Service) History(ctx context.Context, projectID, key, id string) ([]storage.RecordChange, error) {
	if _, _, err := s.Get(ctx, projectID, key, id); err != nil {
		return nil, err
	}
	return s.store.Collections().History(ctx, id, 100)
}

// Query picks records. Ops: eq ne lt lte gt gte contains (a text holds it, or
// a tags field has it) in (one of a list).
type Query struct {
	Where  []storage.Cond `json:"where"`
	Search string         `json:"search"`
	Sort   string         `json:"sort"`
	Desc   bool           `json:"desc"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// Query reads a page of records and how many match.
func (s *Service) Query(ctx context.Context, projectID, key string, q Query) ([]storage.Record, int, error) {
	c, err := s.Collection(ctx, projectID, key)
	if err != nil {
		return nil, 0, err
	}
	rq := storage.RecordQuery{CollectionID: c.ID, Search: q.Search, Desc: q.Desc, Limit: min(q.Limit, MaxPageSize), Offset: q.Offset}
	if q.Sort != "" && q.Sort != "created_at" && q.Sort != "updated_at" {
		if _, ok := field(c, q.Sort); !ok {
			return nil, 0, invalid("không có trường %q để sắp xếp", q.Sort)
		}
	}
	rq.Sort = q.Sort
	for _, w := range q.Where {
		f, ok := field(c, w.Field)
		if !ok {
			return nil, 0, invalid("không có trường %q (%s)", w.Field, fieldNames(c))
		}
		cond := storage.Cond{Field: f.Name, Op: w.Op}
		switch w.Op {
		case "", "eq", "ne", "lt", "lte", "gt", "gte":
			v, err := s.value(ctx, c, f, w.Value, false)
			if err != nil {
				return nil, 0, err
			}
			if f.Type == storage.FieldTags {
				return nil, 0, invalid("trường tags %q: dùng contains", f.Name)
			}
			cond.Value = sqlValue(v)
		case "contains":
			v := fmt.Sprint(w.Value)
			if f.Type == storage.FieldTags {
				cond.Op, cond.Value = "has", strings.TrimSpace(v)
			} else {
				cond.Op, cond.Value = "like", "%"+v+"%"
			}
		case "in":
			vs, ok := w.Value.([]any)
			if !ok {
				return nil, 0, invalid("in cần một danh sách giá trị")
			}
			var out []any
			for _, x := range vs {
				v, err := s.value(ctx, c, f, x, false)
				if err != nil {
					return nil, 0, err
				}
				out = append(out, sqlValue(v))
			}
			cond.Value = out
		default:
			return nil, 0, invalid("phép so sánh %q không có (eq ne lt lte gt gte contains in)", w.Op)
		}
		rq.Where = append(rq.Where, cond)
	}
	return s.store.Collections().Query(ctx, rq)
}

func field(c storage.Collection, n string) (storage.Field, bool) {
	for _, f := range c.Fields {
		if f.Name == n {
			return f, true
		}
	}
	return storage.Field{}, false
}

func fieldNames(c storage.Collection) string {
	var out []string
	for _, f := range c.Fields {
		out = append(out, f.Name)
	}
	return strings.Join(out, ", ")
}

// normalize checks a patch's fields against the schema and puts each value
// in its stored form (nil: take the field out).
func (s *Service) normalize(ctx context.Context, c storage.Collection, data map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range data {
		if k == "id" {
			continue
		}
		f, ok := field(c, k)
		if !ok {
			return nil, invalid("%q không có trường %q (%s)", c.Key, k, fieldNames(c))
		}
		if v == nil {
			out[k] = nil
			continue
		}
		nv, err := s.value(ctx, c, f, v, true)
		if err != nil {
			return nil, err
		}
		out[k] = nv
	}
	return out, nil
}

// value puts one value in its stored form; check: a ref must exist.
func (s *Service) value(ctx context.Context, c storage.Collection, f storage.Field, v any, check bool) (any, error) {
	bad := func(want string) error { return invalid("trường %q cần %s, nhận %v", f.Name, want, v) }
	switch f.Type {
	case storage.FieldNumber:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int:
			return float64(n), nil
		case string:
			x, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil {
				return nil, bad("một số")
			}
			return x, nil
		}
		return nil, bad("một số")
	case storage.FieldBool:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			x, err := strconv.ParseBool(strings.TrimSpace(b))
			if err != nil {
				return nil, bad("true/false")
			}
			return x, nil
		}
		return nil, bad("true/false")
	case storage.FieldTags:
		var list []string
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				list = append(list, fmt.Sprint(x))
			}
		case []string:
			list = t
		case string:
			list = strings.Split(t, ",")
		default:
			return nil, bad("danh sách nhãn")
		}
		out := []any{}
		seen := map[string]bool{}
		for _, x := range list {
			if x = strings.TrimSpace(x); x != "" && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
		return out, nil
	}
	str, ok := v.(string)
	if !ok {
		if n, isNum := v.(float64); isNum && f.Type == storage.FieldText {
			str = strconv.FormatFloat(n, 'f', -1, 64)
		} else {
			return nil, bad("một chuỗi")
		}
	}
	str = strings.TrimSpace(str)
	switch f.Type {
	case storage.FieldDate:
		if t, err := time.Parse(time.RFC3339, str); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
		if _, err := time.Parse(time.DateOnly, str); err == nil {
			return str, nil
		}
		return nil, bad("ngày (2006-01-02 hoặc RFC 3339)")
	case storage.FieldEnum:
		if !slices.Contains(f.Options, str) {
			return nil, bad("một trong " + strings.Join(f.Options, ", "))
		}
	case storage.FieldURL:
		if u, err := url.Parse(str); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, bad("một link http(s)")
		}
	case storage.FieldRef:
		if check {
			rc, err := s.store.Collections().Get(ctx, c.ProjectID, f.Ref)
			if err != nil {
				return nil, invalid("trường %q: không có collection %q", f.Name, f.Ref)
			}
			if _, err := s.store.Collections().Record(ctx, rc.ID, str); err != nil {
				return nil, invalid("trường %q: không có bản ghi %q trong %q", f.Name, str, f.Ref)
			}
		}
	}
	return str, nil
}

// sqlValue is how SQLite's json_extract reads a value back (true → 1).
func sqlValue(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return 1
		}
		return 0
	}
	return v
}

// checkRecord: the required fields are there; the JSON is not too large.
func checkRecord(c storage.Collection, data map[string]any) error {
	for _, f := range c.Fields {
		if v, ok := data[f.Name]; f.Required && (!ok || v == "" || v == nil) {
			return invalid("thiếu trường bắt buộc %q", f.Name)
		}
	}
	if raw, _ := json.Marshal(data); len(raw) > MaxData {
		return invalid("bản ghi quá lớn (%d KB, tối đa %d KB)", len(raw)>>10, MaxData>>10)
	}
	return nil
}

// text is what search reads: the text-like fields' values.
func text(c storage.Collection, data map[string]any) string {
	var parts []string
	for _, f := range c.Fields {
		switch f.Type {
		case storage.FieldText, storage.FieldEnum, storage.FieldURL, storage.FieldFile:
			if s, ok := data[f.Name].(string); ok && s != "" {
				parts = append(parts, s)
			}
		case storage.FieldTags:
			if l, ok := data[f.Name].([]any); ok {
				for _, x := range l {
					parts = append(parts, fmt.Sprint(x))
				}
			}
		}
	}
	out := strings.Join(parts, "\n")
	if strings.ContainsAny(out, "đĐ") { // search ignores accents, but đ is a letter of its own: "doi" finds "đổi" too
		out += "\n" + dd.Replace(out)
	}
	return out
}

var dd = strings.NewReplacer("đ", "d", "Đ", "D")
