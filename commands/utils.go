package commands

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"

	"github.com/h2non/filetype"

	"fb2converter/processor"
)

// isArchiveFile detects if file is our supported archive.
func isArchiveFile(fname string) (bool, error) {

	if !strings.EqualFold(filepath.Ext(fname), ".zip") {
		return false, nil
	}

	file, err := os.Open(fname)
	if err != nil {
		return false, err
	}
	defer file.Close()

	header := make([]byte, 262)
	if count, err := file.Read(header); err != nil {
		return false, err
	} else if count < 262 {
		return false, nil
	}
	return filetype.Is(header, "zip"), nil
}

// isBookFile detects if file is fb2/xml file and if it is tries to detect its encoding.
func isBookFile(fname string) (bool, processor.SrcEncoding, error) {

	if !strings.EqualFold(filepath.Ext(fname), ".fb2") {
		return false, processor.EncUnknown, nil
	}

	file, err := os.Open(fname)
	if err != nil {
		return false, processor.EncUnknown, err
	}
	defer file.Close()

	enc, err := processor.DetectFileUTF(file)
	if err != nil {
		return false, enc, err
	}

	header := make([]byte, 512)
	if _, err := enc.SelectReader(file).Read(header); err != nil {
		return false, processor.EncUnknown, err
	}
	return filetype.Is(header, "fb2"), enc, nil
}

// isBookInArchive detects if compressed file is fb2/xml file and if it is tries to detect its encoding.
func isBookInArchive(f *zip.File) (bool, processor.SrcEncoding, error) {

	if !strings.EqualFold(filepath.Ext(f.FileHeader.Name), ".fb2") {
		return false, processor.EncUnknown, nil
	}

	// zip does not implement io.Seeker, we have to re-open file in archive

	r, err := f.Open()
	if err != nil {
		return false, processor.EncUnknown, err
	}

	buf := []byte{1, 1, 1, 1}
	_, err = r.Read(buf)
	if err != nil {
		r.Close()
		return false, processor.EncUnknown, err
	}
	r.Close()

	enc := processor.DetectUTF(buf)

	r, err = f.Open()
	if err != nil {
		return false, processor.EncUnknown, err
	}
	defer r.Close()

	header := make([]byte, 512)
	if _, err := enc.SelectReader(r).Read(header); err != nil {
		return false, processor.EncUnknown, err
	}
	return filetype.Is(header, "fb2"), enc, nil
}

func init() {
	// Register FB2 matcher for filetype
	filetype.AddMatcher(
		filetype.NewType("fb2", "application/x-fictionbook+xml"),
		func(buf []byte) bool {
			text := string(buf)
			return strings.HasPrefix(text, `<?xml`) && strings.Contains(text, `<FictionBook`)
		})
}
