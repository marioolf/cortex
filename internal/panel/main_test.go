package panel

import (
	"os"
	"testing"

	"github.com/marioolf/cortex/internal/wsl"
)

// No test may run wsl.exe or look into the developer's WSL distros (a POSIX
// working directory of a session makes session.Project ask for them on Windows).
func TestMain(m *testing.M) {
	wsl.SetLookup(func() []string { return nil })
	os.Exit(m.Run())
}
