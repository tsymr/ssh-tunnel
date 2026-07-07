package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"strconv"
)

//go:embed web
var webFS embed.FS

type Server struct {
	mgr     *Manager
	dataDir string
	port    int
}

func newServer(mgr *Manager, dataDir string, port int) *Server {
	return &Server{mgr: mgr, dataDir: dataDir, port: port}
}

func (s *Server) router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/presets", s.handlePresets)

	mux.HandleFunc("GET /api/tunnels", s.handleList)
	mux.HandleFunc("POST /api/tunnels", s.handleCreate)
	mux.HandleFunc("POST /api/tunnels/reorder", s.handleReorder)
	mux.HandleFunc("GET /api/tunnels/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/tunnels/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/tunnels/{id}/start", s.handleStart)
	mux.HandleFunc("POST /api/tunnels/{id}/stop", s.handleStop)
	mux.HandleFunc("GET /api/tunnels/{id}/log", s.handleLog)

	webRoot, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	return mux
}

type statusResp struct {
	Version   string `json:"version"`
	Binary    string `json:"binary"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	DataDir   string `json:"data_dir"`
	Installed bool   `json:"service_installed"`
	Sshpass   bool   `json:"sshpass_available"`
	Total     int    `json:"total"`
	Running   int    `json:"running"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	list := s.mgr.List()
	running := 0
	for _, t := range list {
		if t.Running {
			running++
		}
	}
	bin, _ := os.Executable()
	writeJSON(w, statusResp{
		Version:   version,
		Binary:    bin,
		PID:       os.Getpid(),
		Port:      s.port,
		DataDir:   s.dataDir,
		Installed: serviceInstalled(),
		Sshpass:   commandExists("sshpass"),
		Total:     len(list),
		Running:   running,
	}, 200)
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	out := make([]Tunnel, len(builtinPresets))
	for i, p := range builtinPresets {
		t := p
		t.ID = ""
		out[i] = t
	}
	writeJSON(w, out, 200)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.mgr.List(), 200)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.StatusOf(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, st, 200)
}

type tunnelRequest struct {
	Tunnel
	Password string `json:"password"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req tunnelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	t := req.Tunnel
	if err := s.mgr.Create(&t, req.Password); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, _ := s.mgr.StatusOf(t.ID)
	writeJSON(w, st, 201)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req tunnelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.mgr.Update(id, &req.Tunnel, req.Password); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, _ := s.mgr.StatusOf(id)
	writeJSON(w, st, 200)
}

type reorderRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.mgr.Reorder(req.IDs); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, s.mgr.List(), 200)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Delete(r.PathValue("id")); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Start(r.PathValue("id")); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, _ := s.mgr.StatusOf(r.PathValue("id"))
	writeJSON(w, st, 200)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Stop(r.PathValue("id")); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, _ := s.mgr.StatusOf(r.PathValue("id"))
	writeJSON(w, st, 200)
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	tail := int64(64 * 1024)
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			tail = n
		}
	}
	content, err := s.mgr.Log(r.PathValue("id"), tail)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"log": content}, 200)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, map[string]string{"error": msg}, code)
}
