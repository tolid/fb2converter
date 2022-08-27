package processor

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"

	// additional supported image formats
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/disintegration/imaging"
	"go.uber.org/zap"

	"fb2converter/processor/mobi"
)

type binImageProcessingFlags uint8

const (
	imageKindle binImageProcessingFlags = 1 << iota
	imageOpaquePNG
	imageScale
	imageChanged
)

type binImage struct {
	log *zap.Logger
	//
	id          string
	ct          string
	fname       string
	relpath     string // always relative to "root" directory - usually temporary working directory
	flags       binImageProcessingFlags
	scaleFactor float64
	img         image.Image
	imgType     string
	data        []byte
}

// flush is storing image to file
func (b *binImage) flush(path string) error {

	// Sanity
	if len(b.fname) == 0 || (len(b.data) == 0 && b.img == nil) {
		return nil
	}

	newdir := filepath.Join(path, b.relpath)
	if err := os.MkdirAll(newdir, 0700); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", newdir, err)
	}

	// Do not touch svg images
	if b.imgType == "svg" {
		goto Storing
	}

	// See if processing is needed
	if b.flags != 0 {

		// Just in case
		if b.img == nil && len(b.data) != 0 {
			// image was not decoded yet
			var err error
			b.img, b.imgType, err = image.Decode(bytes.NewReader(b.data))
			if err != nil {
				b.log.Warn("Unable to decode image for processing, storing as is",
					zap.String("id", b.id),
					zap.Error(err))
				goto Storing
			}
		}

		// Scaling
		if b.flags&imageScale != 0 {
			if resizedImg := imaging.Resize(b.img,
				int(float64(b.img.Bounds().Dx())*b.scaleFactor),
				int(float64(b.img.Bounds().Dy())*b.scaleFactor),
				imaging.Linear); resizedImg != nil {
				b.img = resizedImg
			} else {
				b.log.Warn("Unable to resize image, storing as is",
					zap.String("id", b.id))
				goto Storing
			}
		}

		// PNG transparency
		if b.flags&imageOpaquePNG != 0 {

			opaque := func(im image.Image) bool {
				if oimg, ok := im.(interface{ Opaque() bool }); ok {
					return oimg.Opaque()
				}
				return true
			}(b.img)

			if !opaque {
				b.log.Debug("Removing PNG transparency", zap.String("id", b.id))
				opaqueImg := image.NewRGBA(b.img.Bounds())
				draw.Draw(opaqueImg, b.img.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
				draw.Draw(opaqueImg, b.img.Bounds(), b.img, image.Point{}, draw.Over)
				b.img = opaqueImg
			}
		}

		targetType := b.imgType

		// Unsupported format
		if b.flags&imageKindle != 0 {
			if targetType != "jpeg" {
				b.log.Warn("Image type is not supported by targeted device, converting to jpeg",
					zap.String("id", b.id),
					zap.String("type", b.imgType))
				targetType = "jpeg"
			}
		}

		// Serialize the results
		var buf = new(bytes.Buffer)
		switch targetType {
		case "png":
			if err := imaging.Encode(buf, b.img, imaging.PNG); err != nil {
				b.log.Error("Unable to encode processed PNG, skipping",
					zap.String("id", b.id),
					zap.Error(err))
				goto Storing
			}
			b.imgType = "png"
			b.ct = "image/png"
		case "jpeg":
			if err := imaging.Encode(buf, b.img, imaging.JPEG, imaging.JPEGQuality(75)); err != nil {
				b.log.Error("Unable to encode processed image, skipping",
					zap.String("id", b.id),
					zap.Error(err))
				goto Storing
			}
			b.imgType = "jpeg"
			b.ct = "image/jpeg"

			var jfifAdded bool
			buf, jfifAdded = mobi.SetJpegDPI(buf, mobi.DpiPxPerInch, 300, 300)
			if jfifAdded {
				b.log.Debug("Inserting jpeg JFIF APP0 marker segment", zap.String("id", b.id))
			}
		default:
			b.log.Warn("Unable to process image - unsupported format, skipping",
				zap.String("id", b.id),
				zap.String("type", b.imgType))
			goto Storing
		}
		b.data = buf.Bytes()
	}

	// Sanity - should never happen
	if len(b.data) == 0 {
		return fmt.Errorf("no image to save %s (%s)", b.id, filepath.Join(newdir, b.fname))
	}

Storing:
	if err := os.WriteFile(filepath.Join(newdir, b.fname), b.data, 0644); err != nil {
		return fmt.Errorf("unable to save image %s: %w", filepath.Join(newdir, b.fname), err)
	}
	return nil
}
