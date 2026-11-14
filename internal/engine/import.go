package engine

import (
	"errors"

	"github.com/marioolf/cortex/internal/importer"
)

// maxImportErrors is how many failure messages an ImportSummary keeps.
const maxImportErrors = 5

// ImportSummary counts what importing one export did.
type ImportSummary struct {
	Conversations int      `json:"conversations"` // all found in the file: created + updated + unchanged + failed + unreadable
	Created       int      `json:"created"`
	Updated       int      `json:"updated"`
	Unchanged     int      `json:"unchanged"`
	Failed        int      `json:"failed"`     // could not be written
	Unreadable    int      `json:"unreadable"` // could not be decoded, so never reached the vault
	Errors        []string `json:"errors,omitempty"`
}

// Import reads an export (see package importer) and ingests every conversation
// as explicit: the user chose the file, so pause and source switches do not
// apply. A conversation that could not be decoded is counted, not fatal. Any
// other parse error (unsupported file, a zip that breaks half way) is returned,
// together with the summary of what was read before it.
func (e *Engine) Import(path string) (ImportSummary, error) {
	var sum ImportSummary
	sessions, err := importer.Parse(path)
	var sk *importer.SkippedError
	if errors.As(err, &sk) {
		sum.Unreadable = sk.Skipped
		err = nil
	}
	sum.Conversations = len(sessions) + sum.Unreadable
	for _, s := range sessions {
		res, werr := e.Ingest(s, true)
		switch {
		case werr != nil:
			sum.Failed++
			if len(sum.Errors) < maxImportErrors {
				sum.Errors = append(sum.Errors, werr.Error())
			}
		case res == IngestCreated:
			sum.Created++
		case res == IngestUpdated:
			sum.Updated++
		default:
			sum.Unchanged++
		}
	}
	return sum, err
}
