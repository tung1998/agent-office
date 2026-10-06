package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/cleanup"
	"github.com/tung1998/agent-office/internal/storage"
)

// Data management (ADR-095): what each project keeps, and cleaning it — by
// hand or on its own after so many days. Admins only: it deletes for good.
func (s *server) dataRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/data", admin(s.getData))
	mux.Handle("POST /api/data/plan", admin(s.planData))
	mux.Handle("POST /api/data/clean", admin(s.cleanData))
	mux.Handle("POST /api/data/sweep", admin(s.sweepData))
	mux.Handle("PUT /api/data/auto", admin(s.saveDataAuto))
}

type dataItemDTO struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Cleaned   string    `json:"cleaned"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  int       `json:"messages"`
	Bytes     int64     `json:"bytes"`
	Reason    string    `json:"reason,omitempty"` // left as it is: why
}

func toDataItem(it storage.DataItem, reason string) dataItemDTO {
	return dataItemDTO{it.Kind, it.ID, it.ProjectID, it.Title, it.Cleaned, it.UpdatedAt, it.Messages, it.Bytes, reason}
}

type dataStatusDTO struct {
	Running    bool          `json:"running"`
	Level      string        `json:"level"`
	Total      int           `json:"total"`
	Done       int           `json:"done"`
	StartedAt  *time.Time    `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at"`
	Cleaned    int           `json:"cleaned"`
	Bytes      int64         `json:"bytes"`
	Failed     []dataItemDTO `json:"failed"`
}

func toDataStatus(st cleanup.Status) dataStatusDTO {
	d := dataStatusDTO{Running: st.Running, Level: st.Level, Total: st.Total, Done: st.Done, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt, Failed: []dataItemDTO{}}
	if st.Result != nil {
		d.Cleaned, d.Bytes = st.Result.Done, st.Result.Bytes
		for _, f := range st.Result.Failed {
			d.Failed = append(d.Failed, toDataItem(f.DataItem, f.Reason))
		}
	}
	return d
}

type dataUsageDTO struct {
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Items     int    `json:"items"`
	Cleaned   int    `json:"cleaned"`
	Messages  int    `json:"messages"`
	Bytes     int64  `json:"bytes"`
}

func (s *server) getData(w http.ResponseWriter, r *http.Request) {
	usage, err := s.cfg.Store.Data().Usage(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rows := make([]dataUsageDTO, 0, len(usage))
	for _, u := range usage {
		rows = append(rows, dataUsageDTO{u.ProjectID, u.Kind, u.Items, u.Cleaned, u.Messages, u.Bytes})
	}
	projects, err := s.cfg.Store.Repos().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := []map[string]string{}
	for _, p := range projects {
		names = append(names, map[string]string{"id": p.ID, "name": p.Name})
	}
	files, err := s.cfg.Cleanup.Files(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": rows, "projects": names, "files": files,
		"auto": s.cfg.Cleanup.AutoSettings(r.Context()), "status": toDataStatus(s.cfg.Cleanup.Status())})
}

// planData: what a cleanup would take (the first 200 items shown) and what it leaves.
func (s *server) planData(w http.ResponseWriter, r *http.Request) {
	var in cleanup.Request
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Cleanup.Plan(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, skipped := []dataItemDTO{}, []dataItemDTO{}
	for i, it := range p.Items {
		if i == 200 {
			break
		}
		items = append(items, toDataItem(it, ""))
	}
	for _, sk := range p.Skipped {
		skipped = append(skipped, toDataItem(sk.DataItem, sk.Reason))
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(p.Items), "items": items, "skipped": skipped, "bytes": p.Bytes, "messages": p.Messages})
}

// cleanData starts the cleanup; GET /api/data follows it.
func (s *server) cleanData(w http.ResponseWriter, r *http.Request) {
	var in cleanup.Request
	if !decode(w, r, &in) {
		return
	}
	if err := s.cfg.Cleanup.Start(r.Context(), in); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, cleanup.ErrRunning) {
			code = http.StatusConflict
		}
		writeError(w, code, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "data.clean", Resource: "data", ProjectID: in.ProjectID, After: in})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": toDataStatus(s.cfg.Cleanup.Status())})
}

func (s *server) sweepData(w http.ResponseWriter, r *http.Request) {
	freed, err := s.cfg.Cleanup.SweepFiles(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "data.sweep", Resource: "data", After: map[string]any{"bytes": freed}})
	writeJSON(w, http.StatusOK, map[string]any{"bytes": freed})
}

func (s *server) saveDataAuto(w http.ResponseWriter, r *http.Request) {
	var in cleanup.Auto
	if !decode(w, r, &in) {
		return
	}
	before := s.cfg.Cleanup.AutoSettings(r.Context())
	a, err := s.cfg.Cleanup.SetAuto(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "data.auto", Resource: "data", Before: before, After: a})
	writeJSON(w, http.StatusOK, map[string]any{"auto": a})
}
