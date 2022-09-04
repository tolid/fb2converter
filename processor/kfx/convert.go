package kfx

import (
	"bytes"
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

// unpacking KPF which is zipped KDF.
func (c *cnvrtr) unpackKpf(kpf, kdf string) error {

	if err := os.MkdirAll(kdf, 0700); err != nil {
		return fmt.Errorf("unable to create directories for KDF contaner: %w", err)
	}
	if err := archive.Unzip(kpf, kdf); err != nil {
		return fmt.Errorf("unable to unzip KDF contaner (%s): %w", kpf, err)
	}
	return nil
}

// unscrambing book.kdf which is scrambled sqlite3 database.
func (c *cnvrtr) unwrapKdf(kdfBook, sqlFile string) error {

	const (
		wrapperOffset      = 0x400
		wrapperLength      = 0x400
		wrapperFrameLength = 0x100000
	)

	var (
		err         error
		data        []byte
		signature   = []byte("SQLite format 3\x00")
		fingerprint = []byte("\xfa\x50\x0a\x5f")
		header      = []byte("\x01\x00\x00\x40\x20")
	)

	if data, err = os.ReadFile(kdfBook); err != nil {
		return err
	}
	if len(data) <= len(signature) || len(data) < 2*wrapperOffset {
		return fmt.Errorf("unexpected SQLite file length: %d", len(data))
	}
	if !bytes.Equal(signature, data[:len(signature)]) {
		return fmt.Errorf("unexpected SQLite file signature: %v", data[:len(signature)])
	}

	unwrapped := make([]byte, 0, len(data))
	prev, curr := 0, wrapperOffset
	for ; curr+wrapperLength <= len(data); prev, curr = curr+wrapperLength, curr+wrapperLength+wrapperFrameLength {
		if !bytes.Equal(fingerprint, data[curr:curr+len(fingerprint)]) {
			return fmt.Errorf("unexpected fingerprint: %v", data[curr:curr+len(fingerprint)])
		}
		if !bytes.Equal(header, data[curr+len(fingerprint):curr+len(fingerprint)+len(header)]) {
			return fmt.Errorf("unexpected fingerprint header: %v", data[curr+len(fingerprint):curr+len(fingerprint)+len(header)])
		}
		unwrapped = append(unwrapped, data[prev:curr]...)
	}
	unwrapped = append(unwrapped, data[prev:]...)

	if err = os.WriteFile(sqlFile, unwrapped, 0600); err != nil {
		return err
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

	kdfDir := filepath.Join(outDir, DirKdf)
	if err := c.unpackKpf(fromKpf, kdfDir); err != nil {
		return err
	}

	kdfBook := filepath.Join(kdfDir, "resources", "book.kdf")
	sqlFile := filepath.Join(kdfDir, "book.sqlite")
	if err := c.unwrapKdf(kdfBook, sqlFile); err != nil {
		return err
	}

	return fmt.Errorf("FIX ME DONE: ConvertFromKpf")
}
