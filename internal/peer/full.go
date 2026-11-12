package peer

import (
	"context"
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
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	key, err := s.box.Open(p.OutKeyEnc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/api/peer/full-export", nil)
	if err != nil {
		return nil, err
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
		msg := strings.TrimSpace(string(raw))
		if res.StatusCode == http.StatusNotFound {
			msg = "office bên kia chưa mời office này tải (hoặc lời mời đã hết hạn / đã dùng)"
		}
		return nil, fmt.Errorf("office bên kia từ chối (%d): %s", res.StatusCode, msg)
	}
	return res.Body, nil
}
