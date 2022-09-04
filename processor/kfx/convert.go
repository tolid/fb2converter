package kfx

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"fb2converter/archive"
	"fb2converter/state"
)

const (
	DirKdf = "KDF"
)

type cnvrtr struct {
	log *zap.Logger
}

func (c *cnvrtr) unpackKpf(kpf, kdf string) error {

	if err := os.MkdirAll(kdf, 0700); err != nil {
		return fmt.Errorf("unable to create directories for KDF contaner: %w", err)
	}
	// unwrapping KPF which is zipped KDF
	if err := archive.Unzip(kpf, kdf); err != nil {
		return fmt.Errorf("unable to unzip KDF contaner (%s): %w", kpf, err)
	}
	return nil
}

// ConvertFromKpf() takes KPT file and re-packs it to KFX file sutable for Kindle.
func ConvertFromKpf(fromKpf, toKfx, outDir string, env *state.LocalEnv) error {

	start := time.Now()
	env.Log.Debug("Repacking to KFX - start")
	defer func(start time.Time) {
		env.Log.Debug("Repacking to KFX - done",
			zap.Duration("elapsed", time.Since(start)),
			zap.String("from", fromKpf),
			zap.String("to", toKfx),
		)
	}(start)

	c := cnvrtr{
		log: env.Log,
	}

	if err := c.unpackKpf(fromKpf, filepath.Join(outDir, DirKdf)); err != nil {
		return err
	}

	return fmt.Errorf("FIX ME DONE: ConvertFromKpf")
}
