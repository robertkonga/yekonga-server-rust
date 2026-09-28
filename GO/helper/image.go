// Package imageutil resizes and recompresses images in Go.
//
// Pure-Go, no CGo, no C libraries required.
//
// Dependencies:
//
//	go get golang.org/x/image
//	go get github.com/gen2brain/webp
package helper

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	// registers the .webp decoder and provides the WebP encoder
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
)

// ResizeOptions controls resizing and re-encoding behaviour.
type ResizeOptions struct {
	// ── Resize strategy (pick one) ────────────────────────────────────────────
	// Width only  → height auto-scaled to preserve ratio
	// Height only → width  auto-scaled to preserve ratio
	// Both        → exact size (may distort)
	Width  int
	Height int

	// Fit inside a bounding box while preserving ratio.
	// Only applied when Width and Height are both 0.
	MaxWidth  int
	MaxHeight int

	// ScalePercent: e.g. 50 = half size.
	// Only applied when no other size option is set.
	ScalePercent float64

	// ── Encoding ──────────────────────────────────────────────────────────────
	// Quality for JPEG and WebP output (1–100). Default 80.
	// PNG always uses maximum compression (lossless).
	Quality int

	// OutputFormat: "jpeg" | "jpg" | "png" | "webp"
	// Leave empty to keep the source format.
	OutputFormat string

	// Kernel: resampling algorithm.
	//   ""                 / "bilinear"       – fast, good quality (default)
	//   "catmullrom"                          – best quality, slightly slower
	//   "nearestneighbor"                     – fastest, pixelated
	Kernel string
}

// ResizeFile reads src, applies opts, and writes the result to dst.
// Pass dst="" to write next to src (with updated extension when format changes).
func ResizeFile(src, dst string, opts ResizeOptions) error {
	// ── 1. Decode ─────────────────────────────────────────────────────────────
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	img, srcFmt, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("decode %q: %w", src, err)
	}

	origW, origH := img.Bounds().Dx(), img.Bounds().Dy()

	// ── 2. Compute target size ────────────────────────────────────────────────
	targetW, targetH := computeSize(origW, origH, opts)

	// ── 3. Resample ───────────────────────────────────────────────────────────
	var result image.Image
	if targetW == origW && targetH == origH {
		result = img // quality-only reduction, skip resampling
	} else {
		result = resample(img, targetW, targetH, opts.Kernel)
	}

	// ── 4. Resolve output format & path ──────────────────────────────────────
	outFmt := normaliseFormat(opts.OutputFormat, srcFmt)
	if dst == "" {
		dst = replaceExt(src, outFmt)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	// ── 5. Encode & write ─────────────────────────────────────────────────────
	outFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}
	defer outFile.Close()

	quality := opts.Quality
	if quality <= 0 {
		quality = 80
	}

	switch outFmt {
	case "jpeg":
		err = jpeg.Encode(outFile, result, &jpeg.Options{Quality: quality})
	case "png":
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(outFile, result)
	case "webp":
		err = webp.Encode(outFile, result, webp.Options{Quality: quality, Method: webp.DefaultMethod})
	default:
		return fmt.Errorf("unsupported output format %q (use jpeg, png or webp)", outFmt)
	}
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	printReport(src, dst, origW, origH, targetW, targetH)
	return nil
}

// ResizeBatch processes every JPEG / PNG / WebP file in srcDir and writes
// results to dstDir (created automatically).
func ResizeBatch(srcDir, dstDir string, opts ResizeOptions) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read dir %q: %w", srcDir, err)
	}

	supported := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	}

	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supported[ext] {
			continue
		}

		srcPath := filepath.Join(srcDir, e.Name())

		outFmt := normaliseFormat(opts.OutputFormat, strings.TrimPrefix(ext, "."))
		dstPath := filepath.Join(dstDir, replaceExt(e.Name(), outFmt))

		if err := ResizeFile(srcPath, dstPath, opts); err != nil {
			fmt.Fprintf(os.Stderr, "⚠  skip %s: %v\n", e.Name(), err)
			continue
		}
		n++
	}

	fmt.Printf("\nDone — %d image(s) processed.\n", n)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func computeSize(w, h int, o ResizeOptions) (int, int) {
	switch {
	case o.Width > 0 && o.Height > 0:
		return o.Width, o.Height
	case o.Width > 0:
		return o.Width, scale(h, w, o.Width)
	case o.Height > 0:
		return scale(w, h, o.Height), o.Height
	case o.MaxWidth > 0 || o.MaxHeight > 0:
		return fitBox(w, h, o.MaxWidth, o.MaxHeight)
	case o.ScalePercent > 0:
		s := o.ScalePercent / 100
		return rnd(float64(w) * s), rnd(float64(h) * s)
	default:
		return w, h
	}
}

func scale(orig, fixedOrig, fixedNew int) int {
	return rnd(float64(orig) * float64(fixedNew) / float64(fixedOrig))
}

func fitBox(w, h, maxW, maxH int) (int, int) {
	fw, fh := float64(w), float64(h)
	if maxW > 0 && fw > float64(maxW) {
		s := float64(maxW) / fw
		fw, fh = fw*s, fh*s
	}
	if maxH > 0 && fh > float64(maxH) {
		s := float64(maxH) / fh
		fw, fh = fw*s, fh*s
	}
	return rnd(fw), rnd(fh)
}

func rnd(f float64) int { return int(math.Round(f)) }

func resample(src image.Image, w, h int, kernel string) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	var sc draw.Interpolator
	switch strings.ToLower(kernel) {
	case "nearestneighbor", "nearest":
		sc = draw.NearestNeighbor
	case "catmullrom", "catmull":
		sc = draw.CatmullRom
	default:
		sc = draw.BiLinear
	}
	sc.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// normaliseFormat maps raw/source format strings to "jpeg", "png" or "webp".
func normaliseFormat(requested, fallback string) string {
	f := strings.ToLower(requested)
	if f == "" {
		f = strings.ToLower(fallback)
	}
	if f == "jpg" {
		f = "jpeg"
	}
	return f
}

func replaceExt(name, format string) string {
	ext := ".jpg"
	switch format {
	case "png":
		ext = ".png"
	case "webp":
		ext = ".webp"
	}
	return strings.TrimSuffix(name, filepath.Ext(name)) + ext
}

func printReport(src, dst string, ow, oh, nw, nh int) {
	si, _ := os.Stat(src)
	di, _ := os.Stat(dst)
	if si == nil || di == nil {
		return
	}
	pct := 100 - float64(di.Size())/float64(si.Size())*100
	fmt.Printf("✓  %-28s  %dx%d → %dx%d  |  %.1f KB → %.1f KB  (%.0f%% smaller)\n",
		filepath.Base(src),
		ow, oh, nw, nh,
		float64(si.Size())/1024,
		float64(di.Size())/1024,
		pct,
	)
}
