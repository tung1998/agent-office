package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// FileSender is an adapter that can post a file (ADR-083): an image shows as
// one, anything else as a document.
type FileSender interface {
	SendFile(ctx context.Context, chatID, name string, data []byte, caption string) (string, error)
	// MaxFile is the largest file it takes, in bytes.
	MaxFile() int64
}

// IsImage: a file a chat shows as a picture.
func IsImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// form builds a multipart body: the fields, then the file under its field.
func form(fields map[string]string, fileField, name string, data []byte) (*bytes.Buffer, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile(fileField, name)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &b, w.FormDataContentType(), nil
}

// MaxFile: a bot's upload limit on Discord (servers without boosts).
func (d *Discord) MaxFile() int64 { return 10 << 20 }

// SendFile posts a file to a channel or thread, with its caption.
func (d *Discord) SendFile(ctx context.Context, chatID, name string, data []byte, caption string) (string, error) {
	payload, _ := json.Marshal(map[string]any{"content": cut(caption, 1900), "attachments": []map[string]any{{"id": 0, "filename": name}}})
	body, ctype, err := form(map[string]string{"payload_json": string(payload)}, "files[0]", name, data)
	if err != nil {
		return "", err
	}
	base := d.APIBase
	if base == "" {
		base = "https://discord.com/api/v10"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/channels/"+chatID+"/messages", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+d.Token)
	req.Header.Set("Content-Type", ctype)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord gửi file: %s", resp.Status)
	}
	var sent struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&sent)
	return sent.ID, nil
}

// MaxFile: what the Bot API takes as a document (a photo: 10 MB).
func (t *Telegram) MaxFile() int64 { return 50 << 20 }

// SendFile posts an image as a photo (up to 10 MB), anything else as a document.
func (t *Telegram) SendFile(ctx context.Context, chatID, name string, data []byte, caption string) (string, error) {
	if _, err := strconv.ParseInt(chatID, 10, 64); err != nil {
		return "", errors.New("telegram: chat id không hợp lệ")
	}
	method, field := "sendDocument", "document"
	if IsImage(name) && len(data) <= 10<<20 && !strings.EqualFold(filepath.Ext(name), ".gif") {
		method, field = "sendPhoto", "photo"
	}
	body, ctype, err := form(map[string]string{"chat_id": chatID, "caption": cut(caption, 1000)}, field, name, data)
	if err != nil {
		return "", err
	}
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+t.Token+"/"+method, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := t.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // its text has the URL, and the URL has the token
			return "", fmt.Errorf("telegram %s: %v", method, ue.Err)
		}
		return "", fmt.Errorf("telegram %s: lỗi mạng", method)
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || !env.OK {
		return "", fmt.Errorf("telegram %s: %s", method, firstNonEmpty(env.Description, resp.Status))
	}
	return strconv.FormatInt(env.Result.MessageID, 10), nil
}

// SendFileFor posts a file a bot's chat run made or found to that chat (the
// send_file tool, ADR-083): the run's own folder (its worktree or the
// project) only, and nothing that holds secrets.
func (m *Manager) SendFileFor(ctx context.Context, sc actions.Scope, path, caption string) (string, error) {
	channelID, chatID := m.chatOf(ctx, sc)
	if channelID == "" {
		return "", errors.New("send_file chỉ dùng trong cuộc chat của bot Discord/Telegram")
	}
	m.mu.Lock()
	ad := m.adapters[channelID]
	m.mu.Unlock()
	fs, ok := ad.(FileSender)
	if !ok {
		return "", errors.New("bot này đang tắt hoặc không gửi được file")
	}
	file, err := m.allowedFile(ctx, sc, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("không có file %s", path)
	}
	if info.Size() > fs.MaxFile() {
		return "", fmt.Errorf("file %s nặng %d MB, quá giới hạn %d MB của bot", filepath.Base(file), info.Size()>>20, fs.MaxFile()>>20)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if _, err := fs.SendFile(ctx, chatID, filepath.Base(file), data, caption); err != nil {
		return "", err
	}
	return "Đã gửi " + filepath.Base(file) + " vào cuộc chat. Không cần nhắc lại đường dẫn trong câu trả lời.", nil
}

// chatOf is the bot chat a run answers: its job's, else the chat's latest
// job from the bot (a hand-off runs under no job of its own).
func (m *Manager) chatOf(ctx context.Context, sc actions.Scope) (channelID, chatID string) {
	read := func(j storage.Job) bool {
		if !trigger.IsChannel(j.Trigger) {
			return false
		}
		var p trigger.ChannelPayload
		if json.Unmarshal([]byte(j.Payload), &p) != nil || p.ChannelID == "" || p.ChatID == "" {
			return false
		}
		channelID, chatID = p.ChannelID, p.ChatID
		return true
	}
	if sc.JobID != "" {
		if j, err := m.store.Jobs().Get(ctx, sc.JobID); err == nil && read(j) {
			return
		}
	}
	if sc.ConversationID != "" {
		if jobs, err := m.store.Jobs().List(ctx, storage.JobFilter{ConversationID: sc.ConversationID, Limit: 20}); err == nil {
			for _, j := range jobs {
				if read(j) {
					return
				}
			}
		}
	}
	return "", ""
}

// allowedFile is path inside the run's folder, links resolved; never a
// secret's file (.env, keys) and never .git.
func (m *Manager) allowedFile(ctx context.Context, sc actions.Scope, path string) (string, error) {
	var roots []string
	if sc.Dir != "" {
		roots = append(roots, sc.Dir)
	}
	if p, err := m.store.Repos().Get(ctx, sc.ProjectID); err == nil && p.Path != "" {
		roots = append(roots, p.Path)
	}
	if len(roots) == 0 {
		return "", errors.New("lượt này không có thư mục làm việc")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(roots[0], path)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("không có file %s", path)
	}
	base := strings.ToLower(filepath.Base(real))
	if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.Contains(real, string(filepath.Separator)+".git"+string(filepath.Separator)) {
		return "", errors.New("không gửi file bí mật (.env, khóa) hay trong .git")
	}
	for _, r := range roots {
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			if rel, err := filepath.Rel(rr, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return real, nil
			}
		}
	}
	return "", errors.New("chỉ gửi được file trong thư mục làm việc của project (chép file vào đó trước nếu cần)")
}
