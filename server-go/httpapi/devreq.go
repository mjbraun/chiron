package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/mjbraun/chiron/server/devreq"
)

// "Request a change" (SPRITE-DEV-PLAN.md phase G): the app posts what it
// wants changed with a picture of where it was; the agent on the sprite
// takes it from the queue; the app reads the queue to show progress.

func requestsRoot(cfg *Config, root string) string {
	if cfg.RequestsDir != "" {
		return resolve(root, cfg.RequestsDir)
	}
	return filepath.Join(filepath.Dir(root), "state", "requests")
}

type changeRequest struct {
	Text          string          `json:"text"`
	State         json.RawMessage `json:"state,omitempty"`
	ScreenshotB64 string          `json:"screenshot_png_b64,omitempty"`
}

// POST /dev/requests
func (s *Server) handleRequestCreate(w http.ResponseWriter, r *http.Request) {
	var in changeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: %v", err)
		return
	}
	var png []byte
	if in.ScreenshotB64 != "" {
		var err error
		if png, err = base64.StdEncoding.DecodeString(in.ScreenshotB64); err != nil {
			writeError(w, http.StatusBadRequest, "screenshot is not base64")
			return
		}
	}
	req, err := s.requests.Create(in.Text, in.State, png)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// GET /dev/requests
func (s *Server) handleRequestList(w http.ResponseWriter, r *http.Request) {
	list, err := s.requests.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "requests: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GET /dev/requests/{id}
func (s *Server) handleRequestGet(w http.ResponseWriter, r *http.Request) {
	req, err := s.requests.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// POST /dev/requests/{id}/answer: Matt's answer to what the agent asked.
func (s *Server) handleRequestAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: %v", err)
		return
	}
	req, err := s.requests.Answer(r.PathValue("id"), in.Answer)
	switch {
	case errors.Is(err, devreq.ErrNotFound):
		writeError(w, http.StatusNotFound, "%v", err)
	case errors.Is(err, devreq.ErrNotWaiting):
		writeError(w, http.StatusConflict, "%v", err)
	case errors.Is(err, devreq.ErrNoAnswer):
		writeError(w, http.StatusUnprocessableEntity, "%v", err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, "%v", err)
	default:
		writeJSON(w, http.StatusOK, req)
	}
}
