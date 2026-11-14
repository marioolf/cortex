package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/summarize"
	"github.com/marioolf/cortex/internal/vault"
)

const (
	// minSummaryMessages is the shortest conversation worth summarising.
	minSummaryMessages = 4
	// failureBackoff is how long a session that failed is left alone for the
	// same content.
	failureBackoff = time.Hour
	// maxSummaryErrors is how many failures the panel keeps listing; they also
	// expire after summaryErrorTTL, unlike a round's warnings, which every
	// round clears.
	maxSummaryErrors = 5
	summaryErrorTTL  = time.Hour
)

type SummaryPreset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// SummaryState is the summaries part of State.
type SummaryState struct {
	Enabled         bool            `json:"enabled"`
	Tool            string          `json:"tool"`
	Command         []string        `json:"command"` // the effective argv, for display only
	Projects        []string        `json:"projects"`
	IdleMinutes     int             `json:"idle_minutes"` // effective, for the explanation
	MaxPerHour      int             `json:"max_per_hour"` // effective
	Presets         []SummaryPreset `json:"presets"`
	CustomAvailable bool            `json:"custom_available"` // the config file has a command
	Queued          int             `json:"queued"`
	DoneThisHour    int             `json:"done_this_hour"`
	LastError       string          `json:"last_error"`
}

// candidate is a note of a selected project that may need a summary.
type candidate struct {
	project string
	// checked is the note's modification time when it was last found not to
	// need anything (summary up to date, or too short): until the file changes
	// there is nothing to look at again.
	checked time.Time
}

type failure struct {
	hash string
	at   time.Time
}

type summaryError struct {
	msg string
	at  time.Time
}

// sumState is everything the summaries keep; guarded by its own mutex, which
// is never held while the command runs or while taking run.
type sumState struct {
	mu      sync.Mutex
	queue   []queued
	running bool
	starts  []time.Time // when commands were started, for the rate limit
	done    []time.Time
	failed  map[vault.Key]failure
	errs    []summaryError
	lastErr string
	cands   map[vault.Key]*candidate // by source and session ID; touched only while holding run
}

type queued struct {
	id   vault.Key
	hash string
}

// SummariesUsable tells whether the configured tool can be run right now: a
// preset needs its program on PATH, a custom command needs an argv.
func (e *Engine) summariesUsable(s config.Summaries) bool {
	switch s.Tool {
	case "":
		return false
	case config.ToolCustom:
		return len(s.Command) > 0
	}
	p, ok := summarize.PresetByID(s.Tool)
	if !ok {
		return false
	}
	_, err := e.lookPath(p.Argv[0])
	return err == nil
}

func (e *Engine) summariesWanted(cfg *config.Config, project string) bool {
	s := cfg.Summaries
	return s.Enabled && !cfg.Paused && project != "" && slices.Contains(s.Projects, project) && e.summariesUsable(s)
}

// remember notes that the note of a session may need a summary. Call it while
// holding run, after the note was written or found unchanged.
func (e *Engine) remember(cfg *config.Config, id vault.Key, project string) {
	if !e.summariesWanted(cfg, project) {
		return
	}
	if e.sum.cands == nil {
		e.sum.cands = map[vault.Key]*candidate{}
	}
	if c := e.sum.cands[id]; c != nil {
		c.project = project
		return
	}
	e.sum.cands[id] = &candidate{project: project}
}

// scanSummaries queues the candidates whose note has been quiet long enough
// and whose summary is missing or for other content, and starts the worker.
// Call it while holding run.
func (e *Engine) scanSummaries(cfg *config.Config, v *vault.Vault) {
	now := e.now()
	idle := cfg.Summaries.Idle()
	var found []queued
	for id, c := range e.sum.cands {
		if !e.summariesWanted(cfg, c.project) {
			continue
		}
		mod, ok := v.ModTime(id)
		if !ok || mod.Equal(c.checked) || now.Sub(mod) < idle {
			continue
		}
		text, hash, err := v.ConversationText(id)
		if err != nil {
			continue
		}
		if have, ok := v.Summary(id); ok && have == hash || vault.CountMessages(text) < minSummaryMessages {
			c.checked = mod
			continue
		}
		e.sum.mu.Lock()
		f, failedBefore := e.sum.failed[id]
		e.sum.mu.Unlock()
		if failedBefore && f.hash == hash && now.Sub(f.at) < failureBackoff {
			continue
		}
		found = append(found, queued{id, hash})
	}
	e.sum.mu.Lock()
	for _, q := range found {
		i := slices.IndexFunc(e.sum.queue, func(x queued) bool { return x.id == q.id })
		if i >= 0 {
			e.sum.queue[i] = q
		} else {
			e.sum.queue = append(e.sum.queue, q)
		}
	}
	start := len(e.sum.queue) > 0 && !e.sum.running
	if start {
		e.sum.running = true
		e.sumWG.Add(1)
	}
	e.sum.mu.Unlock()
	if start {
		go e.summaryWorker()
	}
}

// summaryWorker runs the queue one summary at a time, outside the round lock.
func (e *Engine) summaryWorker() {
	defer e.sumWG.Done()
	for e.ctx.Err() == nil {
		// e.mu comes before e.sum.mu everywhere (Snapshot does the same).
		e.mu.Lock()
		limit := e.cfg.Summaries.Hourly()
		e.mu.Unlock()
		e.sum.mu.Lock()
		if len(e.sum.queue) == 0 {
			e.sum.running = false
			e.sum.mu.Unlock()
			return
		}
		now := e.now()
		e.sum.starts = recent(e.sum.starts, now)
		if len(e.sum.starts) >= limit {
			// The queue stays; the next round's scan starts a worker again.
			e.sum.running = false
			e.sum.mu.Unlock()
			return
		}
		q := e.sum.queue[0]
		e.sum.queue = e.sum.queue[1:]
		e.sum.starts = append(e.sum.starts, now)
		e.sum.mu.Unlock()

		e.summariseQueued(q)
	}
	e.sum.mu.Lock()
	e.sum.running = false
	e.sum.mu.Unlock()
}

// recent keeps the times from the last hour.
func recent(ts []time.Time, now time.Time) []time.Time {
	return slices.DeleteFunc(ts, func(t time.Time) bool { return now.Sub(t) >= time.Hour })
}

// summariseQueued does one queued item. It takes the round lock only to read
// the conversation and to write the summary.
func (e *Engine) summariseQueued(q queued) {
	e.run.Lock()
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()
	var project string
	if c := e.sum.cands[q.id]; c != nil {
		project = c.project
	}
	if !e.summariesWanted(cfg, project) || e.v == nil {
		e.run.Unlock()
		return
	}
	text, hash, err := e.v.ConversationText(q.id)
	label := e.noteLabel(e.v, q.id)
	e.run.Unlock()
	if err != nil || hash != q.hash {
		return // gone or changed: the next scan decides again
	}

	out, err := e.generate(cfg.Summaries, text)
	if e.ctx.Err() != nil {
		return
	}
	if err != nil {
		e.fail(q.id, hash, label, err)
		return
	}

	e.run.Lock()
	defer e.run.Unlock()
	e.mu.Lock()
	cfg = e.cfg
	e.mu.Unlock()
	if !e.summariesWanted(cfg, project) {
		return // turned off or paused meanwhile: stop, do not write
	}
	_, name, _ := summarize.Resolve(cfg.Summaries.Tool, cfg.Summaries.Command)
	switch err := e.v.WriteSummary(q.id, hash, name, out); {
	case errors.Is(err, vault.ErrStale):
		// The conversation moved on while the tool ran; its summary would describe old text.
	case err != nil:
		e.fail(q.id, hash, label, err)
	default:
		e.sum.mu.Lock()
		e.sum.done = append(recent(e.sum.done, e.now()), e.now())
		delete(e.sum.failed, q.id)
		e.sum.mu.Unlock()
	}
}

// generate runs the configured command on text and returns the cleaned summary.
func (e *Engine) generate(s config.Summaries, text string) (string, error) {
	argv, _, err := summarize.Resolve(s.Tool, s.Command)
	if err != nil {
		return "", err
	}
	r := e.runner
	r.Dir = summarize.WorkDir(e.path)
	out, err := r.Run(e.ctx, argv, summarize.Prompt(text))
	if err != nil {
		return "", err
	}
	return summarize.Clean(out)
}

func (e *Engine) noteLabel(v *vault.Vault, id vault.Key) string {
	if p := v.NotePath(id); p != "" {
		if rel, err := filepath.Rel(v.Root, p); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return id.ID
}

// fail records a failure: no retry for the same content for an hour, and a
// message the panel keeps showing. The message holds the tool's stderr
// excerpt, never the conversation.
func (e *Engine) fail(id vault.Key, hash, label string, err error) {
	now := e.now()
	msg := fmt.Sprintf("summary of %s: %v", label, err)
	e.sum.mu.Lock()
	defer e.sum.mu.Unlock()
	if e.sum.failed == nil {
		e.sum.failed = map[vault.Key]failure{}
	}
	e.sum.failed[id] = failure{hash, now}
	e.sum.errs = append(e.sum.errs, summaryError{msg, now})
	if len(e.sum.errs) > maxSummaryErrors {
		e.sum.errs = e.sum.errs[len(e.sum.errs)-maxSummaryErrors:]
	}
	e.sum.lastErr = msg
}

// summaryErrors lists the failures that have not expired. Call it with e.sum.mu held.
func (e *Engine) summaryErrors() []string {
	now := e.now()
	var out []string
	for _, x := range e.sum.errs {
		if now.Sub(x.at) < summaryErrorTTL {
			out = append(out, x.msg)
		}
	}
	return out
}

// summaryState fills the panel's part. Call it with e.mu held.
func (e *Engine) summaryState() SummaryState {
	cfg := e.cfg.Summaries
	st := SummaryState{
		Enabled: cfg.Enabled, Tool: cfg.Tool, Projects: slices.Clone(cfg.Projects),
		IdleMinutes: int(cfg.Idle() / time.Minute), MaxPerHour: cfg.Hourly(),
		Presets: []SummaryPreset{}, CustomAvailable: len(cfg.Command) > 0 && cfg.Command[0] != "",
		Command: []string{},
	}
	if st.Projects == nil {
		st.Projects = []string{}
	}
	for _, p := range summarize.Presets {
		_, err := e.lookPath(p.Argv[0])
		st.Presets = append(st.Presets, SummaryPreset{p.ID, p.Name, err == nil})
	}
	if argv, _, err := summarize.Resolve(cfg.Tool, cfg.Command); err == nil {
		st.Command = slices.Clone(argv)
	}
	e.sum.mu.Lock()
	defer e.sum.mu.Unlock()
	st.Queued = len(e.sum.queue)
	if e.sum.running {
		st.Queued++
	}
	now := e.now()
	for _, t := range e.sum.done {
		if now.Sub(t) < time.Hour {
			st.DoneThisHour++
		}
	}
	if errs := e.summaryErrors(); len(errs) > 0 {
		st.LastError = errs[len(errs)-1]
	}
	return st
}

// SummarizeNote summarises one note now, for `cortex summarize`: it ignores
// idle time, the project selection, Pause, the rate limit and the on/off
// switch, but needs a tool. path is relative to the vault or absolute inside it.
func (e *Engine) SummarizeNote(path string) (string, error) {
	e.run.Lock()
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()
	v, err := e.vault(cfg)
	if err != nil {
		e.run.Unlock()
		return "", err
	}
	id, err := v.SessionOf(path)
	var text, hash string
	if err == nil {
		text, hash, err = v.ConversationText(id)
	}
	e.run.Unlock()
	if err != nil {
		return "", err
	}
	if vault.CountMessages(text) == 0 {
		return "", errors.New("the note has no messages")
	}
	if !e.summariesUsable(cfg.Summaries) {
		return "", errors.New(`no usable summary tool: set "summaries.tool" in the config file (and its program must be on PATH)`)
	}
	out, err := e.generate(cfg.Summaries, text)
	if err != nil {
		return "", err
	}
	e.run.Lock()
	defer e.run.Unlock()
	_, name, _ := summarize.Resolve(cfg.Summaries.Tool, cfg.Summaries.Command)
	if err := v.WriteSummary(id, hash, name, out); err != nil {
		return "", err
	}
	return out, nil
}

// Stop ends running summaries and waits for them. The engine can still sync.
func (e *Engine) Stop() {
	e.cancel()
	e.sumWG.Wait()
}
