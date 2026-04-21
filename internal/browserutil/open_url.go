package browserutil

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OpenURL launches the system browser without waiting for it to exit.
func OpenURL(url string) error {
	cmd, err := command(url)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func command(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	case "linux", "freebsd", "netbsd", "openbsd":
		providers := []string{"xdg-open", "x-www-browser", "www-browser"}
		for _, provider := range providers {
			if _, err := exec.LookPath(provider); err == nil {
				return exec.Command(provider, url), nil
			}
		}
		return nil, &exec.Error{Name: strings.Join(providers, ","), Err: exec.ErrNotFound}
	default:
		return nil, fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
	}
}
