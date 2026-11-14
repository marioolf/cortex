//go:build !windows

package install

func (system) CreateShortcut(lnk, target string) error { return errUnsupported }
func (system) Start(exe string, args []string) error   { return errUnsupported }
func (system) DeleteAfterExit(target string) error     { return errUnsupported }
