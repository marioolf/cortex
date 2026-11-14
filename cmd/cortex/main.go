// Command cortex saves conversations with AI tools as Markdown notes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marioolf/cortex/internal/autostart"
	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/collector/claudecode"
	"github.com/marioolf/cortex/internal/collector/codex"
	"github.com/marioolf/cortex/internal/collector/copilot"
	"github.com/marioolf/cortex/internal/collector/cursor"
	"github.com/marioolf/cortex/internal/collector/opencode"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/engine"
	"github.com/marioolf/cortex/internal/install"
	"github.com/marioolf/cortex/internal/mcp"
	"github.com/marioolf/cortex/internal/panel"
	"github.com/marioolf/cortex/internal/tray"
)

var version = "dev" // set at release time with -ldflags

const usage = `Usage:
  cortex [run] [-no-browser] [-no-autostart] [-no-tray] [-port N] [-config FILE]
                             run in the background with the control panel
                             (the default when no command is given)
  cortex install [-port N] [-config FILE]
                             Windows: copy Cortex to %LOCALAPPDATA%\Programs\Cortex,
                             add a Start Menu shortcut and start at login, and start it
  cortex uninstall [-purge] [-port N] [-config FILE]
                             Windows: undo install; -purge also removes the config and log
                             (the vault is never removed)
  cortex sync [-vault DIR] [-config FILE]
                             save all conversations found on this machine once
  cortex import FILE [-vault DIR] [-config FILE]
                             save the conversations of a ChatGPT or claude.ai export
                             (the zip as downloaded, or its conversations.json)
  cortex summarize NOTE [-vault DIR] [-config FILE]
                             summarise one note now with the tool set in the config
                             file (sends its text to that tool) and print the summary
  cortex mcp [-vault DIR] [-config FILE]
                             serve the vault to AI tools over MCP on stdin/stdout
  cortex version
`

const (
	defaultPort  = 47821
	syncInterval = 30 * time.Second
)

func main() {
	cmd, args := "run", []string(nil)
	if len(os.Args) > 1 {
		cmd, args = os.Args[1], os.Args[2:]
	}
	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "sync":
		err = syncOnce(args)
	case "import":
		err = importExport(args, os.Stdout, os.Stderr)
	case "summarize":
		err = summarizeNote(args, os.Stdout, os.Stderr)
	case "mcp":
		err = serveMCP(args)
	case "install":
		err = installCmd(args, os.Stdout, os.Stderr, realInstallEnv)
	case "uninstall":
		err = uninstallCmd(args, os.Stdout, os.Stderr, realInstallEnv)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0) // the flag set already printed its usage
	}
	var logged loggedError
	if errors.As(err, &logged) {
		os.Exit(1) // run wrote it to the log and to stderr already
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cortex:", err)
		os.Exit(1)
	}
}

// warnConfig prints the problems of the config file that did not stop the run.
func warnConfig(e *engine.Engine, w io.Writer) {
	for _, m := range e.Snapshot().Warnings {
		fmt.Fprintln(w, "cortex: warning:", m)
	}
}

func collectors() []collector.Collector {
	return []collector.Collector{claudecode.New(), copilot.New(), codex.New(), opencode.New(), cursor.New()}
}

func newEngine(configPath string) (*engine.Engine, error) {
	e, err := engine.New(configPath, collectors())
	if e != nil {
		e.Version = version
	}
	return e, err
}

func syncOnce(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory for this run (default: the configured one, or ~/Cortex)")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	fs.Parse(args)

	e, err := newEngine(*configPath)
	if err != nil {
		return err
	}
	warnConfig(e, os.Stderr)
	if *vaultDir != "" {
		if err := e.OverrideVault(*vaultDir); err != nil {
			return err
		}
	}
	st := e.Sync()
	state := e.Snapshot()
	for _, w := range state.Errors {
		fmt.Fprintln(os.Stderr, "cortex: skipped:", w)
	}
	if state.Paused {
		fmt.Fprintln(os.Stderr, "cortex: paused in the control panel; nothing was saved")
		return nil
	}
	fmt.Printf("%s: %d created, %d updated, %d unchanged", state.Vault, st.Created, st.Updated, st.Unchanged)
	if st.Detached > 0 {
		fmt.Printf(", %d left alone (markers removed)", st.Detached)
	}
	if st.Failed > 0 {
		fmt.Printf(", %d failed", st.Failed)
	}
	fmt.Println()
	if st.Failed > 0 {
		return fmt.Errorf("%d failed", st.Failed) // scripts must be able to tell
	}
	return nil
}

// importExport saves the conversations of an export file. The user chose the
// file, so pause and source switches do not apply (see engine.Ingest).
func importExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vaultDir := fs.String("vault", "", "vault directory for this run (default: the configured one, or ~/Cortex)")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	// The file comes first in the usage line, and flag stops at the first non-flag.
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if file == "" && fs.NArg() == 1 {
		file = fs.Arg(0)
	} else if file == "" || fs.NArg() > 0 {
		return errors.New("usage: cortex import FILE [-vault DIR] [-config FILE]")
	}

	e, err := newEngine(*configPath)
	if err != nil {
		return err
	}
	warnConfig(e, stderr)
	if *vaultDir != "" {
		if err := e.OverrideVault(*vaultDir); err != nil {
			return err
		}
	}
	sum, err := e.Import(file)
	for _, msg := range sum.Errors {
		fmt.Fprintln(stderr, "cortex: failed:", msg)
	}
	if err != nil && sum.Conversations == 0 {
		return err
	}
	fmt.Fprintf(stdout, "%d conversations: %d created, %d updated, %d unchanged, %d failed, %d could not be read\n",
		sum.Conversations, sum.Created, sum.Updated, sum.Unchanged, sum.Failed, sum.Unreadable)
	if err != nil {
		return err // the file broke part way: what was read is saved, but scripts must be able to tell
	}
	if sum.Failed > 0 {
		return fmt.Errorf("%d failed", sum.Failed)
	}
	return nil
}

// summarizeNote summarises one note now. It is for trying a tool out: idle
// time, the project selection and the rate limit do not apply, but a tool must
// be configured. It sends the note's saved text to that tool.
func summarizeNote(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("summarize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vaultDir := fs.String("vault", "", "vault directory for this run (default: the configured one, or ~/Cortex)")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	var note string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		note, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if note == "" && fs.NArg() == 1 {
		note = fs.Arg(0)
	} else if note == "" || fs.NArg() > 0 {
		return errors.New("usage: cortex summarize NOTE [-vault DIR] [-config FILE]")
	}
	e, err := engine.New(*configPath, nil) // no collectors: nothing is read from the AI tools
	if err != nil {
		return err
	}
	defer e.Stop()
	warnConfig(e, stderr)
	if *vaultDir != "" {
		if err := e.OverrideVault(*vaultDir); err != nil {
			return err
		}
	}
	out, err := e.SummarizeNote(note)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, out)
	return nil
}

// serveMCP answers MCP requests on stdin/stdout. It only reads the vault, so it
// does not need `run` to be up, and it must not print anything but protocol
// messages: logs go to stderr.
func serveMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory (default: the configured one, or ~/Cortex)")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	fs.Parse(args)

	dir := *vaultDir
	if dir == "" {
		var cfg *config.Config
		var err error
		if *configPath == "" {
			cfg, err = config.Load()
		} else {
			cfg, err = config.LoadFile(*configPath)
		}
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		dir = cfg.Vault
		for _, m := range cfg.Warnings {
			fmt.Fprintln(os.Stderr, "cortex: warning:", m) // stdout is the protocol
		}
	}
	s := &mcp.Server{Vault: dir, Version: version, Log: log.New(os.Stderr, "cortex mcp: ", log.LstdFlags)}
	s.Log.Printf("serving %s", dir)
	return s.Serve(os.Stdin, os.Stdout)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	noBrowser := fs.Bool("no-browser", false, "do not open the control panel in the browser")
	noAutostart := fs.Bool("no-autostart", false, "on first run, do not register Cortex to start at login")
	noTray := fs.Bool("no-tray", false, "do not show the notification-area icon (Windows)")
	port := fs.Int("port", defaultPort, "port of the control panel on 127.0.0.1")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	fs.Parse(args)
	explicitConfig := *configPath != ""

	if *configPath == "" {
		var err error
		if *configPath, err = config.Path(); err != nil {
			reportFatal(err, "")
			return err
		}
	}
	// The log comes first, before anything that can fail, so that every fatal
	// error below reaches it. A process with no console (Windows) has no
	// stderr, so the file is the only record.
	logPath := filepath.Join(filepath.Dir(*configPath), logName)
	if closeLog, err := setupLog(logPath); err != nil {
		log.Printf("could not open the log file: %v", err)
	} else {
		defer closeLog()
	}
	// First line of every run in the log, whatever else happens after it.
	log.Printf("Cortex %s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)

	err := runCortex(*port, *configPath, explicitConfig, *noBrowser, *noAutostart, *noTray)
	if err != nil {
		log.Printf("fatal: %v", err)
		reportFatal(err, logPath)
		return loggedError{err}
	}
	return nil
}

func runCortex(port int, configPath string, explicitConfig, noBrowser, noAutostart, noTray bool) error {
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/"
	self, _ := os.Executable()
	// Binding the port is the single-instance lock; see claimPort for what
	// happens when a Cortex already holds it.
	ln, existing, err := claimPort(port, configPath, self, 10*time.Second)
	if err != nil {
		return err
	}
	if existing {
		if !noBrowser {
			panel.Open(url)
		}
		return nil
	}
	defer ln.Close()

	// Only Windows installs; elsewhere inst stays nil and nothing changes.
	var inst *install.Installer
	if install.Supported() {
		if inst, err = newInstaller(configPath, "", nil); err != nil {
			log.Printf("install is not available: %v", err)
		}
	}
	var handoff atomic.Bool // set when the installed copy must be started once we are gone

	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		// First run: write the defaults so the file exists to be edited.
		cfg, err := config.LoadFile(configPath)
		if err != nil {
			return err
		}
		if err := cfg.Save(configPath); err != nil {
			return err
		}
		switch {
		case noAutostart:
		case install.Supported() && (inst == nil || !inst.Installed()):
			// Autostart must not point into Downloads: installing registers it.
			log.Printf("not registering Cortex to start at login: it is not installed yet (use Install in the control panel)")
		default:
			if err := autostart.Enable(); err != nil {
				log.Printf("could not register Cortex to start at login: %v", err)
			} else {
				log.Printf("Cortex will start when you log in; turn this off in the control panel")
			}
		}
	}

	e, err := newEngine(configPath)
	if err != nil {
		return err
	}
	for _, w := range e.Snapshot().Warnings {
		log.Printf("warning: %s", w) // the panel shows it at the top as well
	}
	log.Printf("vault %s", e.Snapshot().Vault)
	if e.ExtensionToken() == "" {
		// The browser extension's API stays closed until a pairing code exists.
		code, err := config.NewToken()
		if err != nil {
			return err
		}
		if err := e.Update(func(c *config.Config) { c.ExtensionToken = code }); err != nil {
			return err
		}
	}
	token, err := config.NewToken()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The sync loop and the HTTP server run in goroutines so that the main one
	// is free for the tray, whose message loop wants a thread of its own.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t := time.NewTicker(syncInterval)
		defer t.Stop()
		for {
			if st := e.Sync(); st.Created+st.Updated+st.Failed > 0 {
				log.Printf("sync: %d created, %d updated, %d failed", st.Created, st.Updated, st.Failed)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	opt := panel.Options{Port: port, Quit: stop}
	if inst != nil {
		opt.InstallState = func() panel.InstallState {
			return panel.InstallState{Supported: true, Installed: inst.Installed(),
				RunningFrom: filepath.Dir(inst.Exe), Target: inst.Target}
		}
		opt.Install = func() (string, error) {
			// This process holds the port and is the Cortex to replace, so it
			// does not ask anyone to quit; run starts the new copy once the
			// port is free (see below).
			copied, err := inst.Install(false)
			if !copied && err != nil {
				return "", err
			}
			warning := ""
			if err != nil {
				// The program is in place: hand over anyway, keep what failed
				// (the shortcut or the login entry) in the log and tell the page.
				log.Printf("install: %v", err)
				warning = err.Error()
			}
			log.Printf("installed to %s; handing over", inst.Target)
			handoff.Store(true)
			return warning, nil
		}
	}
	srv := &http.Server{
		Handler:           panel.New(e, token, opt),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
		stop() // a dead server is the end of Cortex, tray or not
	}()
	shutdown := make(chan struct{})
	go func() {
		defer close(shutdown)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	log.Printf("control panel at %s", url)
	if !noBrowser {
		if err := panel.Open(url); err != nil {
			log.Printf("could not open the browser: %v", err)
		}
	}

	// Tray: it blocks until ctx is done, so quitting from the menu, from the
	// panel or by signal all end up here. If it cannot start, Cortex goes on
	// without it.
	if !noTray {
		t := tray.New(tray.Controls{
			Open: func() {
				if err := panel.Open(url); err != nil {
					log.Printf("could not open the browser: %v", err)
				}
			},
			Paused: func() bool { return e.Snapshot().Paused },
			SetPaused: func(p bool) {
				if err := e.Update(func(c *config.Config) { c.Paused = p }); err != nil {
					log.Printf("could not change pause: %v", err)
					return
				}
				go e.Sync() // as in the panel, so the effect shows at once
			},
			Quit: stop,
		})
		if err := t.Run(ctx); err != nil {
			log.Printf("no notification-area icon: %v", err)
		}
	}
	<-ctx.Done()
	<-shutdown
	e.Stop() // a summary command still running is killed
	wg.Wait()

	if handoff.Load() && inst != nil {
		// The port is free now. Without -no-browser the new copy opens the panel.
		a := runArgs(port, configPath, explicitConfig, noTray)
		if err := inst.Sys.Start(inst.Target, a); err != nil {
			log.Printf("could not start the installed copy %s: %v", inst.Target, err)
		}
	}
	return <-serveErr
}

// runArgs is the command line that starts a copy of this run: the same port,
// config and tray choice, and the browser opens (the user just clicked Install).
func runArgs(port int, configPath string, explicitConfig, noTray bool) []string {
	a := []string{"run", "-port", strconv.Itoa(port)}
	if explicitConfig {
		a = append(a, "-config", configPath)
	}
	if noTray {
		a = append(a, "-no-tray")
	}
	return a
}
