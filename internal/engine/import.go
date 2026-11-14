package engine

import (
	"github.com/marioolf/cortex/internal/importer"
	"github.com/marioolf/cortex/internal/session"
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
// apply. Conversations are ingested as they are decoded, so memory does not
// depend on the size of the file. A conversation that could not be used (not
// decodable, too large, no id, an id repeated in the file) is counted, not
// fatal. Any other error (unsupported file, a zip that breaks half way, a size
// cap) is returned, together with the summary of what was read before it.
func (e *Engine) Import(path string) (ImportSummary, error) {
	return e.importWith(path, importer.Options{})
}

func (e *Engine) importWith(path string, opt importer.Options) (ImportSummary, error) {
	var sum ImportSummary
	addErr := func(msg string) {
		if len(sum.Errors) < maxImportErrors {
			sum.Errors = append(sum.Errors, msg)
		}
	}
	opt.Skip = func(reason string) { addErr(reason) }
	unreadable, err := importer.EachWith(path, opt, func(s *session.Session) error {
		sum.Conversations++
		res, werr := e.Ingest(s, true)
		switch {
		case werr != nil:
			sum.Failed++
			addErr(werr.Error())
		case res == IngestCreated:
			sum.Created++
		case res == IngestUpdated:
			sum.Updated++
		default:
			sum.Unchanged++
		}
		return nil
	})
	sum.Unreadable = unreadable
	sum.Conversations += unreadable
	return sum, err
}
