package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"runtime"
	"strconv"
	"strings"
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
// started a newer download while the installed copy runs. A running copy whose
// version is strictly newer than this one is left alone too, so that an old
// download cannot displace a newer install; an unknown version (see
// versionNewer) on either side does not stop the takeover. The reason is for
// the log.
func decideTakeover(runningExe, runningVersion string, pingErr error, selfExe, selfVersion string, same func(a, b string) bool) (takeoverAction, string) {
	switch {
	case pingErr != nil:
		return openExisting, fmt.Sprintf("Cortex is already running; could not ask it which file it runs from (%v), leaving it alone", pingErr)
	case selfExe == "":
		return openExisting, "Cortex is already running; cannot tell which file this one is, leaving it alone"
	case same(runningExe, selfExe):
		return openExisting, fmt.Sprintf("Cortex is already running from this same file (%s)", runningExe)
	case versionNewer(runningVersion, selfVersion):
		return openExisting, fmt.Sprintf("Cortex %s is already running and is newer than this one (%s), leaving it alone", runningVersion, selfVersion)
	}
	return takeOver, fmt.Sprintf("Cortex is already running from a different file (%s, this one is %s): asking it to quit so this one can take over", runningExe, selfExe)
}

// versionNewer reports whether version a is strictly newer than b. Versions are
// dotted numbers ("1.4.2", a leading "v" allowed); anything else, including
// "dev", is unknown and never newer or older.
func versionNewer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parseVersion(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	var parts []int
	for _, f := range strings.Split(v, ".") {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 || strings.HasPrefix(f, "+") {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}

// sameFile compares program paths the way the installer does.
func sameFile(a, b string) bool {
	return install.Same(a, b, runtime.GOOS == "windows", nil)
}

// raceGrace is how long a failed takeover waits for the port to settle before
// deciding who holds it. Tests shorten it.
var raceGrace = 1500 * time.Millisecond

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
	exe, runningVersion, pingErr := panel.RunningExe(url, code, 2*time.Second)
	action, why := decideTakeover(exe, runningVersion, pingErr, self, version, sameFile)
	log.Print(why)
	if action == openExisting {
		return nil, true, nil
	}
	if err := panel.QuitRunning(url, code, wait); err != nil {
		// Two new copies may start at once: the other one can have won, so the
		// old Cortex's quit fails for this one (EOF). Give the port a moment to
		// settle; if a Cortex holds it then, it is "already running".
		for deadline := time.Now().Add(raceGrace); ; time.Sleep(100 * time.Millisecond) {
			if ln, lerr := net.Listen("tcp", addr); lerr == nil {
				log.Printf("the previous Cortex has quit; starting")
				return ln, false, nil
			}
			if time.Now().After(deadline) {
				break
			}
		}
		if panel.Running(url, 2*time.Second) {
			log.Printf("Cortex is already running (could not replace it: %v)", err)
			return nil, true, nil
		}
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
