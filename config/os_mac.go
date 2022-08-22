//go:build darwin

package config

import (
	"os"
	"strings"
)

// CleanFileName removes not allowed characters form file name.
func CleanFileName(in string) string {
	out := strings.TrimLeft(strings.Map(func(sym rune) rune {
		if strings.IndexRune(string(os.PathSeparator)+string(os.PathListSeparator), sym) != -1 {
			return -1
		}
		return sym
	}, in), ".")
	if len(out) == 0 {
		out = "_bad_file_name_"
	}
	return out
}

// FindConverter  - used on Windows to support myhomelib
func FindConverter(_ string) string {
	return ""
}

// kindlegen provides OS specific part of default kindlegen location
func kindlegen() string {
	return "kindlegen"
}

// kpvDefault returns os specific path where kindle previewer is installed by default.
func kpvDefault() (string, error) {
	return "", ErrNoKindlePreviewer
}

// kpvExec executes kpv - we need this as Windows requires special handling.
func kpvExec(exepath string, stdouter func(string), arg ...string) error {
	return ErrNoKindlePreviewer
}
