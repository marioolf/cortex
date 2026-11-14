// Command cortex saves conversations with AI tools as Markdown notes.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/marioolf/cortex/internal/collector"
	"github.com/marioolf/cortex/internal/collector/claudecode"
	"github.com/marioolf/cortex/internal/config"
	"github.com/marioolf/cortex/internal/redact"
	"github.com/marioolf/cortex/internal/session"
	"github.com/marioolf/cortex/internal/vault"
)

var version = "dev" // set at release time with -ldflags

const usage = `Usage:
  cortex sync [-vault DIR]   save all conversations found on this machine
  cortex version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		if err := sync(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "cortex:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func sync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory (default: the configured one, or ~/Cortex)")
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *vaultDir != "" {
		cfg.Vault = *vaultDir
	}
	red, err := redact.New(cfg.RedactPatterns)
	if err != nil {
		return err
	}
	v, err := vault.Open(cfg.Vault)
	if err != nil {
		return err
	}

	var counts [4]int
	failed := 0
	warn := func(err error) {
		failed++
		fmt.Fprintln(os.Stderr, "cortex: skipped:", err)
	}
	for _, c := range []collector.Collector{claudecode.New()} {
		if !cfg.SourceEnabled(c.ID()) {
			continue
		}
		c.Collect(func(s *session.Session) {
			if cfg.ProjectExcluded(session.Project(s.Cwd)) {
				return
			}
			s.Title = red.Apply(s.Title)
			for i := range s.Messages {
				s.Messages[i].Text = red.Apply(s.Messages[i].Text)
			}
			res, err := v.Write(s)
			if err != nil {
				warn(fmt.Errorf("%s %s: %w", s.Source, s.ID, err))
				return
			}
			counts[res]++
		}, warn)
	}
	fmt.Printf("%s: %d created, %d updated, %d unchanged", cfg.Vault, counts[vault.Created], counts[vault.Updated], counts[vault.Unchanged])
	if n := counts[vault.Detached]; n > 0 {
		fmt.Printf(", %d left alone (markers removed)", n)
	}
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	return nil
}
