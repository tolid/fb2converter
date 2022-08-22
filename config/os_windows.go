//go:build windows

package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"fb2converter/config/winpty"
)

// CleanFileName removes not allowed characters form file name.
func CleanFileName(in string) string {
	out := strings.Map(func(sym rune) rune {
		if strings.ContainsRune(`<>":/\|?*`+string(os.PathSeparator)+string(os.PathListSeparator), sym) {
			return -1
		}
		return sym
	}, in)
	if len(out) == 0 {
		out = "_bad_file_name_"
	}
	return out
}

// FindConverter attempts to find main conversion engine - myhomelib support.
func FindConverter(expath string) string {

	var err error
	if len(expath) == 0 {
		expath, err = os.Executable()
		if err != nil {
			return ""
		}
	}

	wd := filepath.Dir(expath)

	paths := []string{
		filepath.Join(wd, "fb2c.exe"),                               // `pwd`
		filepath.Join(filepath.Dir(wd), "fb2converter", "fb2c.exe"), // `pwd`/../fb2converter
	}

	for _, p := range paths {
		if _, err = os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// kindlegen provides OS specific part of default kindlegen location
func kindlegen() string {
	return "kindlegen.exe"
}

// kpvDefault returns OS specific path where Kindle Previewer is installed by default.
func kpvDefault() (string, error) {
	res, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("unable to find local AppData folder: %w", err)
	}
	return filepath.Join(res, "Amazon", "Kindle Previewer 3", "Kindle Previewer 3.exe"), nil
}

// kpvExec executes kpv from provided path using winpty to intercept output.
// NOTE: on Windows kpv attaches to console and directly writes to screen buffer, so reading stdout does not
// work - we have to use screenscraper (WinPTY).
func kpvExec(exepath string, stdouter func(string), arg ...string) error {

	expath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get program path: %w", err)
	}

	pty, err := winpty.New(filepath.Dir(expath))
	if err != nil {
		return fmt.Errorf("failed to allocate winpty: %w", err)
	}

	err = pty.Open()
	if err != nil {
		return fmt.Errorf("failed to open winpty: %w", err)
	}
	defer pty.Close()

	_ = pty.SetWinsize(200, 60)

	if stdouter != nil {
		go func() {
			// read kpv stdout
			scanner := bufio.NewScanner(pty.StdOut)
			for scanner.Scan() {
				stdouter(scanner.Text())
			}
		}()
	}

	cmd := exec.Command(exepath, arg...)
	err = pty.Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to run winpty: %w", err)
	}

	err = pty.Wait()
	if err != nil {
		var exitCode uint32
		winptyError, ok := err.(*winpty.ExitError)
		if ok {
			exitCode = winptyError.WaitStatus.ExitCode
		} else {
			exitError, ok := err.(*exec.ExitError)
			if !ok {
				return fmt.Errorf("kindle previewer failed with unexpected error: %w", err)
			}
			waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok {
				return fmt.Errorf("kindle previewer failed with unexpected status: %w", err)
			}
			if waitStatus.Signaled() {
				return errors.New("kindle previewer was interrupted")
			}
			exitCode = uint32(waitStatus.ExitStatus())
		}
		return fmt.Errorf("kindle previewer ended with code %d", exitCode)
	}
	return nil
}
