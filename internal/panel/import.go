package panel

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/marioolf/cortex/internal/engine"
	"github.com/marioolf/cortex/internal/importer"
)

// defaultMaxImport caps an uploaded export. Real ones can be hundreds of MB.
const defaultMaxImport = 2 << 30

// importFile receives an export as the raw request body (the page sends the
// File itself, so the browser streams it from disk), spools it to a temp file
// because the importer needs random access to a zip, ingests it as an explicit
// import and always removes the file again.
func (p *panel) importFile(w http.ResponseWriter, r *http.Request) {
	if !p.importing.CompareAndSwap(false, true) {
		jsonError(w, "another import is running; wait for it to finish", http.StatusConflict)
		return
	}
	defer p.importing.Store(false)

	f, err := os.CreateTemp(p.opt.TempDir, "cortex-import-*") // 0600
	if err != nil {
		jsonError(w, "cannot create a temporary file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, p.opt.MaxImport))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			jsonError(w, "the file is too large to import", http.StatusRequestEntityTooLarge)
			return
		}
		jsonError(w, "could not receive the file: "+err.Error(), http.StatusBadRequest)
		return
	}

	sum, err := p.e.Import(f.Name())
	if errors.Is(err, importer.ErrUnsupported) || (err != nil && sum.Conversations == 0) {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := struct {
		engine.ImportSummary
		Warning string `json:"warning,omitempty"` // the file broke part way; what was read before is saved
	}{ImportSummary: sum}
	if err != nil {
		out.Warning = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
