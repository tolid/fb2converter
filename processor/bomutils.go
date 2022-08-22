package processor

import (
	"fmt"
	"io"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
	"golang.org/x/text/transform"
)

type SrcEncoding int

const (
	EncUnknown SrcEncoding = iota
	EncUTF8
	EncUTF16BigEndian
	EncUTF16LittleEndian
	EncUTF32BigEndian
	EncUTF32LittleEndian
)

// SelectReader handles various unicode encodings (with or without BOM).
func (enc SrcEncoding) SelectReader(r io.Reader) io.Reader {
	switch enc {
	case EncUnknown:
		return r
	case EncUTF8:
		return transform.NewReader(r, unicode.BOMOverride(unicode.UTF8.NewDecoder()))
	case EncUTF16BigEndian:
		return transform.NewReader(r, unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder())
	case EncUTF16LittleEndian:
		return transform.NewReader(r, unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder())
	case EncUTF32BigEndian:
		return transform.NewReader(r, utf32.UTF32(utf32.BigEndian, utf32.ExpectBOM).NewDecoder())
	case EncUTF32LittleEndian:
		return transform.NewReader(r, utf32.UTF32(utf32.LittleEndian, utf32.ExpectBOM).NewDecoder())
	default:
		panic("unsupported encoding - should never happen")
	}
}

func isUTF32BigEndianBOM4(buf []byte) bool {
	return buf[0] == 0x00 && buf[1] == 0x00 && buf[2] == 0xFE && buf[3] == 0xFF
}

func isUTF32LittleEndianBOM4(buf []byte) bool {
	return buf[0] == 0xFF && buf[1] == 0xFE && buf[2] == 0x00 && buf[3] == 0x00
}

func isUTF8BOM3(buf []byte) bool {
	return buf[0] == 0xEF && buf[1] == 0xBB && buf[2] == 0xBF
}

func isUTF16BigEndianBOM2(buf []byte) bool {
	return buf[0] == 0xFE && buf[1] == 0xFF
}

func isUTF16LittleEndianBOM2(buf []byte) bool {
	return buf[0] == 0xFF && buf[1] == 0xFE
}

// DetectUTF attempts to detect encoding of passed in sequence of bytes.
func DetectUTF(buf []byte) (enc SrcEncoding) {

	if isUTF32BigEndianBOM4(buf) {
		return EncUTF32BigEndian
	}
	if isUTF32LittleEndianBOM4(buf) {
		return EncUTF32LittleEndian
	}
	if isUTF8BOM3(buf) {
		return EncUTF8
	}
	if isUTF16BigEndianBOM2(buf) {
		return EncUTF16BigEndian
	}
	if isUTF16LittleEndianBOM2(buf) {
		return EncUTF16LittleEndian
	}
	return EncUnknown
}

// DetectFileUTF attempts to detect encoding on a file preserving file position.
func DetectFileUTF(file io.ReadSeeker) (SrcEncoding, error) {

	var enc SrcEncoding

	// remember position
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return enc, err
	}
	// rewind to the beginning
	if ref, err := file.Seek(0, io.SeekStart); err != nil {
		return enc, err
	} else if ref != 0 {
		return enc, fmt.Errorf("unable to rewind file: %d != 0", ref)
	}
	// read BOM
	buf := []byte{1, 1, 1, 1}
	if _, err = file.Read(buf); err != nil {
		return enc, err
	}
	enc = DetectUTF(buf)
	// restore position
	if ref, err := file.Seek(offset, io.SeekStart); err != nil {
		return enc, err
	} else if ref != offset {
		return enc, fmt.Errorf("unable to reset file: %d != %d", ref, offset)
	}
	return enc, nil
}
