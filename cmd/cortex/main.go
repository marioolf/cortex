// Command cortex saves conversations with AI tools as Markdown notes.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/marioolf/cortex/internal/autostart"
	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/collector/claudecode"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/engine"
	"github.com/marioolf/cortex/internal/panel"
)

var version = "dev" // set at release time with -ldflags

const usage = `Usage:
  cortex [run] [-no-browser] [-no-autostart] [-port N] [-config FILE]
                             run in the background with the control panel
                             (the default when no command is given)
  cortex sync [-vault DIR] [-config FILE]
                             save all conversations found on this machine once
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
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cortex:", err)
		os.Exit(1)
	}
}

func collectors() []collector.Collector {
	return []collector.Collector{claudecode.New()}
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
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	noBrowser := fs.Bool("no-browser", false, "do not open the control panel in the browser")
	noAutostart := fs.Bool("no-autostart", false, "on first run, do not register Cortex to start at login")
	port := fs.Int("port", defaultPort, "port of the control panel on 127.0.0.1")
	configPath := fs.String("config", "", "config file (default: the one in the OS config directory)")
	fs.Parse(args)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(*port))
	url := "http://" + addr + "/"

	// Binding the port is the single-instance lock: whoever holds it is Cortex.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if c, derr := net.DialTimeout("tcp", addr, time.Second); derr == nil {
			c.Close()
			log.Printf("Cortex is already running at %s", url)
			if !*noBrowser {
				panel.Open(url)
			}
			return nil
		}
		return err
	}
	defer ln.Close()

	if *configPath == "" {
		if *configPath, err = config.Path(); err != nil {
			return err
		}
	}
	if _, err := os.Stat(*configPath); errors.Is(err, os.ErrNotExist) {
		// First run: write the defaults so the file exists to be edited.
		cfg, err := config.LoadFile(*configPath)
		if err != nil {
			return err
		}
		if err := cfg.Save(*configPath); err != nil {
			return err
		}
		if !*noAutostart {
			if err := autostart.Enable(); err != nil {
				log.Printf("could not register Cortex to start at login: %v", err)
			} else {
				log.Printf("Cortex will start when you log in; turn this off in the control panel")
			}
		}
	}

	e, err := newEngine(*configPath)
	if err != nil {
		return err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(raw[:])

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
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

	srv := &http.Server{
		Handler:           panel.New(e, token, panel.Options{Port: *port}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	log.Printf("control panel at %s", url)
	if !*noBrowser {
		if err := panel.Open(url); err != nil {
			log.Printf("could not open the browser: %v", err)
		}
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
