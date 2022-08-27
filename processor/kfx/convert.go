package kfx

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"fb2converter/state"
)

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

	return fmt.Errorf("FIX ME DONE: ConvertFromKpf")
}
