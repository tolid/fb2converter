package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/blang/semver"
)

var (
	// ErrNoKindlePreviewer - not all platforms have Kindle Previewer.
	ErrNoKindlePreviewer = errors.New("kindle previewer 3 is not supported for this OS/platform")

	verRE           = regexp.MustCompile(`^Kindle\s+Previewer\s+([0-9]+\.[0-9]+\.[0-9]+)\s+Copyright\s+\(c\)\s+Amazon\.com.*$`)
	verMinSupported = semver.Version{Major: 3, Minor: 66, Patch: 0}
)

// KindlePreviewerEnv has everything necessary to run kindle previewer in command line mode and process results.
type KindlePreviewerEnv struct {
	version semver.Version
	path    string
}

// String returns debug information for current environment.
func (e *KindlePreviewerEnv) String() string {
	return fmt.Sprintf("%s (%s)", e.path, e.version)
}

// Exec runs Kindle Previewer with specified arguments.
func (e *KindlePreviewerEnv) Exec(logger func(s string), arg ...string) error {
	if err := kpvExec(e.path, logger, arg...); err != nil {
		return fmt.Errorf("unable to run kindle previewer [%s]: %w", e.path, err)
	}
	return nil
}

// NewKindlePreviewerEnv initializes new KindlePreviewerEnv.
func (conf *Config) NewKindlePreviewerEnv() (*KindlePreviewerEnv, error) {

	var (
		err error
		ver semver.Version
	)

	kpath := conf.Doc.KindlePreviewer.Path
	if len(kpath) > 0 {
		if !filepath.IsAbs(kpath) {
			return nil, fmt.Errorf("path to kindle previewer must be absolute path [%s]", kpath)
		}
	} else {
		kpath, err = kpvDefault()
		if err != nil {
			return nil, fmt.Errorf("problem getting kindle previewer path: %w", err)
		}
	}
	if _, err = os.Stat(kpath); err != nil {
		return nil, fmt.Errorf("unable to find kindle previewer [%s]: %w", kpath, err)
	}

	out := make([]string, 0, 32)
	if err = kpvExec(kpath, func(s string) {
		if len(s) > 0 {
			out = append(out, s)
		}
	}, "-help"); err != nil {
		return nil, fmt.Errorf("unable to run kindle previewer [%s]: %w", kpath, err)
	}

	for _, s := range out {
		matches := verRE.FindStringSubmatch(s)
		if len(matches) < 2 {
			continue
		}
		if ver, err = semver.Parse(matches[1]); err != nil {
			return nil, fmt.Errorf("unable to parse kindle previewer version: %w", err)
		}
		break
	}
	if ver.EQ(semver.Version{}) {
		return nil, errors.New("unable to find kindle previewer version")
	}
	if verMinSupported.GT(ver) {
		return nil, fmt.Errorf("unsupported version %s of kindle previewer is installed (required version %s or newer)", ver, verMinSupported)
	}

	kpv := &KindlePreviewerEnv{
		version: ver,
		path:    kpath,
	}
	return kpv, nil
}
