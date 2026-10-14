package httpapi

import (
	"net/http"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/files"
)

type deletePermanentRequest struct {
	Paths   []string `json:"paths"`
	Confirm string   `json:"confirm,omitempty"`
}

// handleFilesDeletePermanent is Shift+Delete (SPEC §8.5 "Hapus permanen"):
// bypasses the Recycle Bin; > 50 items need confirm:"HAPUS".
func (s *Server) handleFilesDeletePermanent(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req deletePermanentRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	if len(paths) > files.PermanentConfirmThreshold && req.Confirm != files.PermanentConfirmWord {
		WriteError(w, r, CodeValidationFailed, 0, "confirmation word required for large batches",
			map[string]any{"fields": map[string]any{"confirm": "required"}, "word": files.PermanentConfirmWord, "threshold": files.PermanentConfirmThreshold})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.DeletePermanent(r.Context(), j, u.ID, paths)
	if res != nil && res.Deleted > 0 {
		s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionDeletePermanent, TargetType: "path",
			TargetID: res.Results[0].Path, IP: ClientIP(r), UserAgent: r.UserAgent(),
			Details: map[string]any{"count": res.Deleted, "first": res.Results[0].Path, "bytesFreed": res.BytesFreed, "source": "browser"}})
	}
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	out := s.opView(r, j, u.ID, &res.Result, "")
	out["deleted"] = res.Deleted
	out["bytesFreed"] = res.BytesFreed
	WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleFilesDirSize(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req pathRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.DirSize(r.Context(), j, u.ID, p)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

type checksumRequest struct {
	Path   string   `json:"path"`
	Algos  []string `json:"algos,omitempty"`
	Verify *struct {
		Algo     string `json:"algo"`
		Expected string `json:"expected"`
	} `json:"verify,omitempty"`
}

func (s *Server) handleFilesChecksum(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req checksumRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	algos := req.Algos
	if req.Verify != nil {
		algos = append(algos, req.Verify.Algo)
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	sums, err := s.files.Checksum(r.Context(), j, u.ID, p, algos)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	out := map[string]any{"path": string(p)}
	for k, v := range sums {
		out[k] = v
	}
	if req.Verify != nil {
		match, actual, verr := s.files.VerifyChecksum(r.Context(), j, u.ID, p, req.Verify.Algo, req.Verify.Expected)
		if verr != nil {
			s.writeOpsError(w, r, verr)
			return
		}
		out["verified"] = match
		out["actual"] = actual
	}
	WriteJSON(w, http.StatusOK, out)
}

type checksumVerifyRequest struct {
	Path     string `json:"path"`
	Algo     string `json:"algo"`
	Expected string `json:"expected"`
}

func (s *Server) handleFilesChecksumVerify(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req checksumVerifyRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	if req.Expected == "" {
		WriteError(w, r, CodeValidationFailed, 0, "expected hash is required", map[string]any{"fields": map[string]any{"expected": "required"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	match, actual, err := s.files.VerifyChecksum(r.Context(), j, u.ID, p, req.Algo, req.Expected)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"path": string(p), "algo": req.Algo, "match": match, "actual": actual})
}
