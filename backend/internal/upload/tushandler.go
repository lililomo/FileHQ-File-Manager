package upload

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"

	"github.com/tus/tusd/v2/pkg/handler"
)

// Handler is the tusd UnroutedHandler with HEAD/PATCH overridden so that an
// upload whose bytes are all stored but whose finalisation failed on the last
// PATCH (423 lock conflict, transient error) is finalised on the client's
// retry (review C M-01). tusd itself answers HEAD/PATCH of a complete upload
// from the store without running the pre-finish hook again, so without this
// the file would never land and the session would stay active until the
// cleanup job dropped it 24 h later.
type Handler struct {
	*handler.UnroutedHandler
	svc *Service
}

// HeadFile finalises a complete, still-active upload of the caller before
// delegating to tusd; the response mirrors tusd's (200 + Upload-Offset) plus
// the Upload-Result/Upload-Path headers of a normal finish.
func (h *Handler) HeadFile(w http.ResponseWriter, r *http.Request) {
	if h.finishStored(w, r) {
		return
	}
	h.UnroutedHandler.HeadFile(w, r)
}

// PatchFile is HeadFile's counterpart for the empty retry PATCH at the final
// offset (204 + Upload-Offset).
func (h *Handler) PatchFile(w http.ResponseWriter, r *http.Request) {
	if h.finishStored(w, r) {
		return
	}
	h.UnroutedHandler.PatchFile(w, r)
}

// finishStored reports true when it wrote the response itself.
func (h *Handler) finishStored(w http.ResponseWriter, r *http.Request) bool {
	id := path.Base(r.URL.Path)
	if id == "" || id == "/" || id == "." {
		return false
	}
	res, ok, err := h.svc.FinishStored(r.Context(), id)
	if !ok {
		return false
	}
	hd := w.Header()
	hd.Set("Tus-Resumable", "1.0.0")
	hd.Set("Cache-Control", "no-store")
	if err != nil {
		var te handler.Error
		if errors.As(err, &te) {
			for k, v := range te.HTTPResponse.Header {
				hd.Set(k, v)
			}
			w.WriteHeader(te.HTTPResponse.StatusCode)
			_, _ = io.WriteString(w, te.HTTPResponse.Body)
			return true
		}
		h.svc.o.Logger.ErrorContext(r.Context(), "upload finalisation failed", slog.String("error", err.Error()))
		hd.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"INTERNAL","message":"internal server error"}}`)
		return true
	}
	for k, v := range res.Header {
		hd.Set(k, v)
	}
	hd.Set("Upload-Offset", strconv.FormatInt(res.Size, 10))
	if r.Method == http.MethodHead {
		hd.Set("Upload-Length", strconv.FormatInt(res.Size, 10))
		w.WriteHeader(http.StatusOK)
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}
