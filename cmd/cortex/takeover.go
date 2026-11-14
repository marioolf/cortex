package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"runtime"
	"strconv"
	"time"

	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/install"
	"github.com/marioolf/cortex/internal/panel"
)

type takeoverAction int

const (
	openExisting takeoverAction = iota // leave the running Cortex alone and show its panel
	takeOver                           // ask it to quit and run in its place
)

// decideTakeover chooses what a Cortex that finds another one on its port
// does. runningExe is what the running one said about itself; pingErr is set
// when it could not be asked (older version, wrong or missing pairing code).
// Only a running copy that is a different file is replaced: the person has
// started a newer download while the installed copy runs. The reason is for
// the log.
func decideTakeover(runningExe string, pingErr error, selfExe string, same func(a, b string) bool) (takeoverAction, string) {
	switch {
	case pingErr != nil:
		return openExisting, fmt.Sprintf("Cortex is already running; could not ask it which file it runs from (%v), leaving it alone", pingErr)
	case selfExe == "":
		return openExisting, "Cortex is already running; cannot tell which file this one is, leaving it alone"
	case same(runningExe, selfExe):
		return openExisting, fmt.Sprintf("Cortex is already running from this same file (%s)", runningExe)
	}
	return takeOver, fmt.Sprintf("Cortex is already running from a different file (%s, this one is %s): asking it to quit so this one can take over", runningExe, selfExe)
}

// sameFile compares program paths the way the installer does.
func sameFile(a, b string) bool {
	return install.Same(a, b, runtime.GOOS == "windows", nil)
}

// claimPort takes the single-instance lock on 127.0.0.1:port. When a Cortex
// holds it, see decideTakeover: existing is true if it was left alone (the
// caller opens its panel and exits), otherwise the old one was asked to quit
// and ln is the listener once the port was free (waiting up to wait for it).
func claimPort(port int, configPath, self string, wait time.Duration) (ln net.Listener, existing bool, err error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	url := "http://" + addr + "/"
	if ln, err = net.Listen("tcp", addr); err == nil {
		return ln, false, nil
	}
	// The holder may be some other program: only one that answers like our panel is Cortex.
	if !panel.Running(url, 2*time.Second) {
		return nil, false, fmt.Errorf("port %d is taken by another program; use -port to pick a different one", port)
	}
	code, _, perr := config.Pairing(configPath)
	if perr != nil {
		log.Printf("could not read the pairing code: %v", perr)
	}
	exe, pingErr := panel.RunningExe(url, code, 2*time.Second)
	action, why := decideTakeover(exe, pingErr, self, sameFile)
	log.Print(why)
	if action == openExisting {
		return nil, true, nil
	}
	if err := panel.QuitRunning(url, code, wait); err != nil {
		return nil, false, fmt.Errorf("could not replace the running Cortex: %w", err)
	}
	for deadline := time.Now().Add(wait); ; time.Sleep(100 * time.Millisecond) {
		if ln, err = net.Listen("tcp", addr); err == nil {
			log.Printf("the previous Cortex has quit; starting")
			return ln, false, nil
		}
		if time.Now().After(deadline) {
			return nil, false, errors.New("the previous Cortex quit but its port is still not free: " + err.Error())
		}
	}
}
