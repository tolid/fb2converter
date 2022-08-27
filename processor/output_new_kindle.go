package processor

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"fb2converter/processor/kfx"
)

// FinalizeKFX produces final KFX file out of previously saved temporary files.
func (p *Processor) FinalizeKFX(fname string) error {

	outDir := filepath.Join(p.tmpDir, DirKfx)
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return fmt.Errorf("unable to create data directories for Kindle Previewer: %w", err)
	}

	kpf, err := p.generateKindlePreviewerContent(outDir)
	if err != nil {
		return fmt.Errorf("unable to generate intermediate content: %w", err)
	}
	if _, err := os.Stat(fname); err == nil {
		if !p.overwrite {
			return fmt.Errorf("output file already exists: %s", fname)
		}
		p.env.Log.Warn("Overwriting existing file", zap.String("file", fname))
		if err = os.Remove(fname); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.MkdirAll(filepath.Dir(fname), 0700); err != nil {
		return fmt.Errorf("unable to create output directory: %w", err)
	}

	return kfx.ConvertFromKpf(kpf, fname, outDir, p.env)
}

// generateKindlePreviewerContent produces temporary KPF file by running Kindle Previewer and returns its full path.
func (p *Processor) generateKindlePreviewerContent(outDir string) (string, error) {

	args := make([]string, 0, 10)
	args = append(args, filepath.Join(p.tmpDir, DirEpub, DirContent, "content.opf"))
	args = append(args, "-convert")
	args = append(args, "-locale", "en")
	args = append(args, "-output", outDir)

	start := time.Now()
	p.env.Log.Debug("Kindle Previewer - start")
	defer func(start time.Time) {
		p.env.Log.Debug("Kindle Previewer - done",
			zap.Duration("elapsed", time.Since(start)),
			zap.Stringer("kpv", p.kpv),
			zap.Strings("args", args),
		)
	}(start)

	if err := p.kpv.Exec(func(s string) {
		p.env.Log.Debug("Kindle Previewer", zap.String("stdout", s))
	}, args...); err != nil {
		return "", err
	}
	book, err := checkResults(outDir, p.env.Log)
	if err != nil {
		return "", err
	}
	return book, nil
}

func checkResults(outDir string, log *zap.Logger) (string, error) {

	var (
		err     error
		csvFile *os.File
		csvName = filepath.Join(outDir, "Summary_Log.csv")
	)

	if csvFile, err = os.Open(csvName); err != nil {
		return "", fmt.Errorf("unable to open conversion summary: %w", err)
	}
	defer csvFile.Close()

	const (
		hdrBookName int = iota // "Book Name" - input
		hdrETStatus            // "Enhanced Typesetting Status"
		hdrStatus              // "Conversion Status"
		hdrErrors              // "Error Count"
		hdrInfo                // "Quality Issue Count"
		hdrBook                // "Output File Path" - output
		hdrLog                 // "Log File Path"
		hdrReport              // "Quality Report Path"
	)

	enc, err := DetectFileUTF(csvFile)
	if err != nil {
		return "", fmt.Errorf("unable to read conversion summary: %w", err)
	}

	r := csv.NewReader(enc.SelectReader(csvFile))
	r.FieldsPerRecord = 0

	records, err := r.ReadAll()
	if err != nil {
		return "", fmt.Errorf("unable to parse conversion summary: %w", err)
	}
	if len(records) != 2 {
		return "", fmt.Errorf("wrong number of summary lines: %d", len(records))
	}

	headers := records[0]
	record := records[1]

	var fields = []zap.Field{}
	for i := 0; i < len(headers); i++ {
		fields = append(fields, zap.String(headers[i], record[i]))
	}
	log.Debug("Kindle Previwer summary", fields...)

	if !strings.EqualFold(record[hdrETStatus], "Supported") {
		return "", fmt.Errorf("wrong Enhanced Typesetting Status: %s", record[hdrETStatus])
	}
	if !strings.EqualFold(record[hdrStatus], "Success") {
		return "", fmt.Errorf("wrong Conversion Status: %s", record[hdrStatus])
	}
	if !strings.EqualFold(record[hdrErrors], "0") {
		return "", errors.New("errors during conversion, see log for details")
	}
	// Make sure we are picking file path from proper column, sometime around 3.55 number of columt changed and
	// resulting diagnostic was confising at best: not a zip file.
	if !strings.EqualFold(headers[hdrBook], "Output File Path") {
		return "", errors.New("unable to detect resulting KPF path, possible kindle viewer version change")
	}
	if len(record[hdrBook]) == 0 {
		return "", errors.New("unable to detect resulting KPF, path is empty")
	}
	if _, err = os.Stat(record[hdrBook]); err != nil {
		return "", fmt.Errorf("unable to find resulting KPF file [%s]: %w", record[hdrBook], err)
	}
	return record[hdrBook], nil
}
