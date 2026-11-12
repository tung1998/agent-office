package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FetchFull opens the whole-office export another office offered this one
// (ADR-275): a stream, read without the 30 s limit of the other calls (it
// may be gigabytes). The caller closes it.
func (s *Service) FetchFull(ctx context.Context, id string) (io.ReadCloser, error) {
	return s.Stream(ctx, id, http.MethodGet, "/api/peer/full-export", nil, nil)
}

// Stream calls path on paired office id with no time limit, body as is
// (an archive), and returns its answer's body when it is a 2xx. The caller
// closes it.
func (s *Service) Stream(ctx context.Context, id, method, path string, body io.Reader, header http.Header) (io.ReadCloser, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	key, err := s.box.Open(p.OutKeyEnc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Transport: s.client.Transport, CheckRedirect: s.client.CheckRedirect}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không gọi được office bên kia: %w", err)
	}
	if res.StatusCode/100 != 2 {
		defer res.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if res.StatusCode == http.StatusNotFound && msg == "" {
			msg = "office bên kia chưa có tính năng này (cần cập nhật office đó)"
		}
		return nil, fmt.Errorf("office bên kia từ chối (%d): %s", res.StatusCode, msg)
	}
	return res.Body, nil
}
