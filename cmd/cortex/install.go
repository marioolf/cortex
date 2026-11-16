package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/install"
	"github.com/marioolf/cortex/internal/panel"
)

// installEnv is what the install commands need from the system, so tests can
// replace it: the OS name and the way to build an Installer.
type installEnv struct {
	goos string
	new  func(configPath string, port int) (*install.Installer, error)
}

var realInstallEnv = installEnv{goos: runtime.GOOS, new: func(configPath string, port int) (*install.Installer, error) {
	// Only the pairing code and the vault are needed, and a damaged config must
	// not stop anyone from uninstalling: whatever cannot be read is reported
	// and skipped.
	token, vault, problem := config.Pairing(configPath)
	if problem != nil {
		fmt.Fprintf(os.Stderr, "cortex: warning: %v; going on without the pairing code (a running Cortex may have to be quit by hand)\n", problem)
	} else if cfg, err := config.LoadFile(configPath); err == nil {
		for _, w := range cfg.Warnings {
			fmt.Fprintln(os.Stderr, "cortex: warning:", w)
		}
	}
	if vault == "" {
		if home, err := os.UserHomeDir(); err == nil {
			vault = filepath.Join(home, "Cortex")
		}
	}
	url := "http://" + panelAddr(port) + "/"
	// A running Cortex is asked to quit with the pairing code from the config file.
	quit := func() error { return panel.QuitRunning(url, token, 10*time.Second) }
	return newInstaller(configPath, vault, quit)
}}

func panelAddr(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

// errManual is what install and uninstall answer where there is no installer.
var errManual = errors.New("cortex install is for Windows; on this system use the installer instead: curl -fsSL https://raw.githubusercontent.com/marioolf/cortex/main/install.sh | sh (or put the cortex binary somewhere on your PATH; starting at login is handled from the control panel)")

// newInstaller describes the installation of the running program. quit may be
// nil when the caller never asks another Cortex to stop.
func newInstaller(configPath, vault string, quit func() error) (*install.Installer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	target, lnk, err := install.Locations(os.Getenv)
	if err != nil {
		return nil, err
	}
	if quit == nil {
		quit = func() error { return nil }
	}
	return &install.Installer{
		Exe: exe, Target: target, Shortcut: lnk,
		ConfigFile: configPath, LogFile: filepath.Join(filepath.Dir(configPath), logName), Vault: vault,
		FoldCase: runtime.GOOS == "windows",
		Sys:      install.NewSystem(quit),
	}, nil
}

// installFlags parses the flags both commands share and finds the config file.
func installFlags(name string, args []string, stderr io.Writer, extra func(*flag.FlagSet)) (port int, configPath string, explicit bool, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	p := fs.Int("port", defaultPort, "port of the control panel of a running Cortex")
	c := fs.String("config", "", "config file (default: the one in the OS config directory)")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return 0, "", false, err
	}
	if fs.NArg() > 0 {
		return 0, "", false, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	configPath = *c
	if configPath == "" {
		if configPath, err = config.Path(); err != nil {
			return 0, "", false, err
		}
	}
	return *p, configPath, *c != "", nil
}

func installCmd(args []string, stdout, stderr io.Writer, env installEnv) error {
	if env.goos != "windows" {
		return errManual
	}
	port, configPath, explicit, err := installFlags("install", args, stderr, nil)
	if err != nil {
		return err
	}
	in, err := env.new(configPath, port)
	if err != nil {
		return err
	}
	wasInstalled := in.Installed()
	copied, ierr := in.Install(true)
	if ierr != nil && !copied && !wasInstalled {
		return ierr // nothing was done
	}
	// From here on a failing step is reported at the end, not instead of the
	// steps after it: a missing shortcut must not keep the installed copy from starting.
	problems := []error{}
	if ierr != nil {
		problems = append(problems, ierr)
	}
	if !copied {
		if ierr == nil {
			fmt.Fprintf(stdout, "Cortex is already installed at %s; the Start Menu shortcut and start at login were refreshed.\n", in.Target)
		}
		return errors.Join(problems...) // this is the running copy: nothing to hand over to
	}
	fmt.Fprintf(stdout, "Installed Cortex to %s\n", in.Target)
	if ierr == nil {
		fmt.Fprintf(stdout, "Added the Start Menu shortcut %s and registered Cortex to start at login.\n", in.Shortcut)
	}
	// The panel opens in the browser, as after a first start.
	if err := in.Sys.Start(in.Target, runArgs(port, configPath, explicit, false)); err != nil {
		problems = append(problems, fmt.Errorf("Cortex was installed to %s, but could not be started: %w (start it from the Start Menu)", in.Target, err))
	} else {
		fmt.Fprintln(stdout, "Started it; the control panel opens in your browser. You can delete the file you ran this from.")
	}
	return errors.Join(problems...)
}

func uninstallCmd(args []string, stdout, stderr io.Writer, env installEnv) error {
	if env.goos != "windows" {
		return errManual
	}
	var purge *bool
	port, configPath, _, err := installFlags("uninstall", args, stderr, func(fs *flag.FlagSet) {
		purge = fs.Bool("purge", false, "also remove the config file and the log (the vault is never removed)")
	})
	if err != nil {
		return err
	}
	in, err := env.new(configPath, port)
	if err != nil {
		return err
	}
	rep, err := in.Uninstall(*purge)
	for _, l := range rep.Removed {
		fmt.Fprintln(stdout, "removed:", l)
	}
	for _, l := range rep.Left {
		fmt.Fprintln(stdout, "left in place:", l)
	}
	return err
}
