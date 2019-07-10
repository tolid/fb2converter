// Package commands has top level command drivers.
package commands

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"go.uber.org/zap"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"

	"github.com/rupor-github/fb2converter/archive"
	"github.com/rupor-github/fb2converter/config"
	"github.com/rupor-github/fb2converter/processor"
	"github.com/rupor-github/fb2converter/state"
)

// processBook processes single FB2 file. "src" is part of the source path (always including file name) relative to the original
// path. When actual file was specified it will be just base file name without a path. When looking inside archive or directory
// it will be relative path inside archive or directory (including base file name).
func processBook(r io.Reader, enc srcEncoding, src, dst string, nodirs, stk, overwrite bool, format processor.OutputFmt, env *state.LocalEnv) error {

	var fname string

	start := time.Now()
	env.Log.Info("Conversion starting", zap.String("from", src))
	defer func(start time.Time) {
		if r := recover(); r != nil {
			env.Log.Error("Conversion ended with panic", zap.Duration("elapsed", time.Now().Sub(start)), zap.String("to", fname), zap.ByteString("stack", debug.Stack()))
		} else {
			env.Log.Info("Conversion completed", zap.Duration("elapsed", time.Now().Sub(start)), zap.String("to", fname))
		}
	}(start)

	p, err := processor.NewFB2(selectReader(r, enc), enc == encUnknown, src, dst, nodirs, stk, overwrite, format, env)
	if err != nil {
		return err
	}
	if err = p.Process(); err != nil {
		return err
	}
	if fname, err = p.Save(); err != nil {
		return err
	}
	if err = p.SendToKindle(fname); err != nil {
		return err
	}
	return p.Clean()
}

// processDir walks directory tree finding fb2 files and processes them.
func processDir(dir string, format processor.OutputFmt, nodirs, stk, overwrite bool, cpage encoding.Encoding, dst string, env *state.LocalEnv) (err error) {

	count := 0
	defer func() {
		if err == nil && count == 0 {
			env.Log.Debug("Nothing to process", zap.String("dir", dir))
		}
	}()

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			env.Log.Warn("Skipping path", zap.String("path", path), zap.Error(err))
		} else if info.Mode().IsRegular() {
			var enc srcEncoding
			if ok, err := isArchiveFile(path); err != nil {
				// checking format - but cannot open target file
				env.Log.Warn("Skipping file", zap.String("file", path), zap.Error(err))
			} else if ok {
				if err := processArchive(path, "", filepath.Dir(strings.TrimPrefix(path, dir)), format, nodirs, stk, overwrite, cpage, dst, env); err != nil {
					env.Log.Error("Unable to process archive", zap.String("file", path), zap.Error(err))
				}
			} else if ok, enc, err = isBookFile(path); err != nil {
				env.Log.Warn("Skipping file", zap.String("file", path), zap.Error(err))
			} else if ok {
				count++
				// encoding will be handled properly by processBook
				if file, err := os.Open(path); err != nil {
					env.Log.Error("Unable to process file", zap.String("file", path), zap.Error(err))
				} else {
					defer file.Close()
					if err := processBook(file, enc,
						strings.TrimPrefix(strings.TrimPrefix(path, dir), string(filepath.Separator)), dst,
						nodirs, stk, overwrite, format, env); err != nil {

						env.Log.Error("Unable to process file", zap.String("file", path), zap.Error(err))
					}
				}
			}
		}
		return nil
	})
	return err
}

// processArchive walks all files inside archive, finds fb2 files under "pathIn" and processes them.
func processArchive(path, pathIn, pathOut string, format processor.OutputFmt, nodirs, stk, overwrite bool, cpage encoding.Encoding, dst string, env *state.LocalEnv) (err error) {

	count := 0
	defer func() {
		if err == nil && count == 0 {
			env.Log.Debug("Nothing to process", zap.String("archive", path))
		}
	}()

	err = archive.Walk(path, pathIn, func(archive string, f *zip.File) error {
		if ok, enc, err := isBookInArchive(f); err != nil {
			env.Log.Warn("Skipping file in archive",
				zap.String("archive", archive),
				zap.String("path", f.FileHeader.Name),
				zap.Error(err))
		} else if ok {
			count++
			// encoding will be handled properly by processBook
			if r, err := f.Open(); err != nil {
				env.Log.Error("Unable to process file in archive",
					zap.String("archive", archive),
					zap.String("file", f.FileHeader.Name),
					zap.Error(err))
			} else {
				defer r.Close()

				// TODO: should we split pathOut into parts and decode each one separatly here?
				path := filepath.Join(pathOut, f.FileHeader.Name)
				if cpage != nil && f.FileHeader.NonUTF8 {
					// forcing zip file name encoding
					if n, err := cpage.NewDecoder().String(path); err == nil {
						path = n
					} else {
						n, _ = ianaindex.IANA.Name(cpage)
						env.Log.Warn("Unable to convert archive name from specified encoding", zap.String("charset", n), zap.String("path", path), zap.Error(err))
					}
				}
				if err := processBook(r, enc, path, dst, nodirs, stk, overwrite, format, env); err != nil {
					env.Log.Error("Unable to process file in archive",
						zap.String("archive", archive),
						zap.String("file", f.FileHeader.Name),
						zap.Error(err))
				}
			}
		}
		return nil
	})
	return err
}

// Convert is "convert" command body.
func Convert(ctx *cli.Context) (err error) {

	const (
		errPrefix = "\n*** ERROR ***\n\nconvert: "
		errCode   = 1
	)

	env := ctx.GlobalGeneric(state.FlagName).(*state.LocalEnv)

	src := ctx.Args().Get(0)
	if len(src) == 0 {
		return cli.NewExitError(errors.New(errPrefix+"no input source has been specified"), errCode)
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return cli.NewExitError(errors.Wrapf(err, "%scleaning source path failed", errPrefix), errCode)
	}

	dst := ctx.Args().Get(1)
	if len(dst) == 0 {
		if dst, err = os.Getwd(); err != nil {
			return cli.NewExitError(errors.Wrapf(err, "%sunable to get working directory", errPrefix), errCode)
		}
	} else {
		if dst, err = filepath.Abs(dst); err != nil {
			return cli.NewExitError(errors.Wrapf(err, "%scleaning destination path failed", errPrefix), errCode)
		}
	}

	var format processor.OutputFmt
	if env.Mhl == config.MhlMobi {
		format = processor.ParseFmtString(env.Cfg.Fb2Mobi.OutputFormat)
		if format == processor.UnsupportedOutputFmt || format == processor.OEpub || format == processor.OKepub {
			env.Log.Warn("Unknown output format in MHL mode requested, switching to mobi", zap.String("format", env.Cfg.Fb2Mobi.OutputFormat))
			format = processor.OMobi
		}
	} else if env.Mhl == config.MhlEpub {
		format = processor.ParseFmtString(env.Cfg.Fb2Epub.OutputFormat)
		if format == processor.UnsupportedOutputFmt || format == processor.OMobi || format == processor.OAzw3 {
			env.Log.Warn("Unknown output format in MHL mode requested, switching to epub", zap.String("format", env.Cfg.Fb2Epub.OutputFormat))
			format = processor.OEpub
		}
	} else {
		format = processor.ParseFmtString(ctx.String("to"))
		if format == processor.UnsupportedOutputFmt {
			env.Log.Warn("Unknown output format requested, switching to epub", zap.String("format", ctx.String("to")))
			format = processor.OEpub
		}
	}

	nodirs := ctx.Bool("nodirs")
	overwrite := ctx.Bool("ow")

	var cpage encoding.Encoding

	page := ctx.String("force-zip-cp")
	if len(page) > 0 {
		cpage, err = ianaindex.IANA.Encoding(page)
		if err != nil {
			env.Log.Warn("Unknown character set specification. Ignoring...", zap.String("charset", page), zap.Error(err))
			cpage = nil
		} else {
			n, _ := ianaindex.IANA.Name(cpage)
			env.Log.Debug("Forcefully convert all non UTF-8 file names in archives", zap.String("charset", n))
		}
	}

	stk := ctx.Bool("stk")
	if env.Mhl == config.MhlMobi {
		stk = env.Cfg.Fb2Mobi.SendToKindle
	}
	if stk && format != processor.OMobi {
		env.Log.Warn("Send to Kindle could only be used with mobi output format, turning off", zap.Stringer("format", format))
		stk = false
	}

	start := time.Now()
	env.Log.Info("Processing starting", zap.String("source", src), zap.String("destination", dst), zap.Stringer("format", format))
	defer func(start time.Time) {
		env.Log.Info("Processing completed", zap.Duration("elapsed", time.Now().Sub(start)))
	}(start)

	var head, tail string
	for head = src; len(head) != 0; head, tail = filepath.Split(head) {

		head = strings.TrimSuffix(head, string(filepath.Separator))

		fi, err := os.Stat(head)
		if err != nil {
			// does not exists - probably path in archive
			continue
		}

		if fi.Mode().IsDir() {
			if len(tail) != 0 {
				// directory cannot have tail - it would be simple file
				return cli.NewExitError(
					errors.Errorf("%sinput source was not found (%s) => (%s)", errPrefix, head, strings.TrimPrefix(src, head)),
					errCode)
			}
			if err := processDir(head, format, nodirs, stk, overwrite, cpage, dst, env); err != nil {
				return cli.NewExitError(errors.Wrapf(err, "%sunable to process directory", errPrefix), errCode)
			}
			break
		}

		if fi.Mode().IsRegular() {

			ok, err := isArchiveFile(head)
			if err != nil {
				// checking format - but cannot open target file
				return cli.NewExitError(errors.Wrapf(err, "%sunable to check archive type", errPrefix), errCode)
			}

			if ok {
				// we need to look inside to see if path makes sense
				tail = strings.TrimPrefix(strings.TrimPrefix(src, head), string(filepath.Separator))
				if err := processArchive(head, tail, "", format, nodirs, stk, overwrite, cpage, dst, env); err != nil {
					return cli.NewExitError(errors.Wrapf(err, "%sunable to process archive", errPrefix), errCode)
				}
				break
			}

			var enc srcEncoding
			ok, enc, err = isBookFile(head)
			if err != nil {
				// checking format - but cannot open target file
				return cli.NewExitError(errors.Wrapf(err, "%sunable to check file type", errPrefix), errCode)

			}

			if ok && len(tail) == 0 {
				// we have book, it cannot have tail
				// encoding will be handled properly by processBook
				if file, err := os.Open(head); err != nil {
					env.Log.Error("Unable to process file", zap.String("file", head), zap.Error(err))
				} else {
					defer file.Close()
					if err := processBook(file, enc, filepath.Base(head), dst, nodirs, stk, overwrite, format, env); err != nil {
						env.Log.Error("Unable to process file", zap.String("file", head), zap.Error(err))
					}
				}
				break
			}

			return cli.NewExitError(
				errors.Errorf("%sinput was not recognized as FB2 book (%s)", errPrefix, head),
				errCode)
		}

		return cli.NewExitError(
			errors.Errorf("%sunexpected path mode for (%s) => (%s)", errPrefix, head, strings.TrimPrefix(src, head)),
			errCode)
	}
	if len(head) == 0 {
		return cli.NewExitError(errors.Errorf("%sinput source was not found (%s)", errPrefix, src), errCode)
	}

	return nil
}
