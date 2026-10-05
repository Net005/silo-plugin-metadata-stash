package poster

import (
	"bytes"
	_ "embed"
	"fmt"
	pigo "github.com/esimov/pigo/core"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"math"
	"strings"
)

type face struct {
	X     int     `json:"x"`
	Y     int     `json:"y"`
	W     int     `json:"w"`
	H     int     `json:"h"`
	Score float32 `json:"score"`
}

func posterResize(src image.Image, w, h int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.BiLinear.Scale(out, out.Bounds(), src, src.Bounds(), stdraw.Src, nil)
	return out
}
func posterCrop(src image.Image, r image.Rectangle) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	stdraw.Draw(out, out.Bounds(), src, r.Min, stdraw.Src)
	return out
}
func posterText(dst *image.RGBA, line string, y int, scale int) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: float64(scale * 12), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	defer face.Close()
	d := font.Drawer{Dst: dst, Src: image.White, Face: face, Dot: fixed.P(30, y+scale*12)}
	d.DrawString(line)
}

//go:embed facefinder
var posterFaceCascade []byte

// RenderScenePoster never synthesises imagery. The default retains the full source.
func RenderScenePoster(raw []byte, title, footer, mode string) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 30000000 {
		return nil, fmt.Errorf("invalid or oversized poster source")
	}
	im, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	ratio := float64(config.Width) / float64(config.Height)
	if ratio > .62 && ratio < .71 {
		var out bytes.Buffer
		err := jpeg.Encode(&out, posterResize(im, 600, 900), &jpeg.Options{Quality: 90})
		return out.Bytes(), err
	}
	c, err := pigo.NewPigo().Unpack(posterFaceCascade)
	if err != nil {
		return nil, err
	}
	bounds := im.Bounds()

	detectorImage := im
	detectorScale := 1.0
	if bounds.Dx() > 400 {
		detectorScale = 400 / float64(bounds.Dx())
		detectorImage = posterResize(im, 400, max(1, int(float64(bounds.Dy())*detectorScale)))
	}
	ds := c.ClusterDetections(c.RunCascade(pigo.CascadeParams{MinSize: 30, MaxSize: 600, ShiftFactor: .1, ScaleFactor: 1.1, ImageParams: pigo.ImageParams{Pixels: pigo.RgbToGrayscale(detectorImage), Rows: detectorImage.Bounds().Dy(), Cols: detectorImage.Bounds().Dx(), Dim: detectorImage.Bounds().Dx()}}, 0), .2)
	faces := []face{}
	for _, d := range ds {
		if d.Q >= 10 {
			faces = append(faces, face{int(float64(d.Col-d.Scale/2) / detectorScale), int(float64(d.Row-d.Scale/2) / detectorScale), int(float64(d.Scale) / detectorScale), int(float64(d.Scale) / detectorScale), d.Q})
		}
	}

	requestMode := mode
	mode = "full-frame composition"

	source := im
	rect := bounds
	w, h := bounds.Dx(), bounds.Dy()
	if float64(w)/float64(h) > .62 && float64(w)/float64(h) < .71 {
		mode = "preserve portrait"

	} else if requestMode == "face" && len(faces) == 1 {
		a := faces[0]
		cw, ch := int(math.Min(float64(w), float64(h)*2/3)), h
		if float64(w)/float64(h) < 2.0/3 {
			cw = w
			ch = w * 3 / 2
		}
		x := max(0, min(w-cw, a.X+a.W/2-cw/2))
		y := max(0, min(h-ch, a.Y+a.H/2-int(float64(ch)*.35)))
		candidate := image.Rect(x, y, x+cw, y+ch)
		margin := int(float64(a.W) * .08)
		expanded := image.Rect(a.X-margin, a.Y-margin, a.X+a.W+margin, a.Y+a.H+margin)
		if expanded.In(candidate) {
			mode = "face-aware crop"

			rect = candidate
			source = posterCrop(im, rect)
		} else {

		}
	} else if len(faces) > 1 {

	}
	out := image.NewRGBA(image.Rect(0, 0, 600, 900))
	if mode == "face-aware crop" || mode == "preserve portrait" {
		out = posterResize(source, 600, 900)
	} else {
		// Blur at a tiny resolution: bounded work, no GPU or extra image library.
		bg := posterResize(im, 60, 90)
		for pass := 0; pass < 3; pass++ {
			tmp := image.NewRGBA(bg.Bounds())
			for y := 0; y < 90; y++ {
				for x := 0; x < 60; x++ {
					rr, gg, bb, n := 0, 0, 0, 0
					for yy := max(0, y-3); yy <= min(89, y+3); yy++ {
						for xx := max(0, x-3); xx <= min(59, x+3); xx++ {
							v := bg.RGBAAt(xx, yy)
							rr += int(v.R)
							gg += int(v.G)
							bb += int(v.B)
							n++
						}
					}
					tmp.SetRGBA(x, y, color.RGBA{uint8(rr / n), uint8(gg / n), uint8(bb / n), 255})
				}
			}
			bg = tmp
		}
		for y := 0; y < 90; y++ {
			for x := 0; x < 60; x++ {
				v := bg.RGBAAt(x, y)
				bg.SetRGBA(x, y, color.RGBA{v.R / 3, v.G / 3, v.B / 3, 255})
			}
		}
		out = posterResize(bg, 600, 900)
		scale := math.Min(600/float64(w), 620/float64(h))
		fw, fh := int(float64(w)*scale), int(float64(h)*scale)
		fg := posterResize(im, fw, fh)
		stdraw.Draw(out, image.Rect((600-fw)/2, 70+(620-fh)/2, (600+fw)/2, 70+(620+fh)/2), fg, image.Point{}, stdraw.Src)
	}
	// Raster title/footer for a proper portrait card; no generated imagery.
	for y := 700; y < 900; y++ {
		alpha := uint32((y - 700) * 230 / 200)
		for x := 0; x < 600; x++ {
			v := out.RGBAAt(x, y)
			out.SetRGBA(x, y, color.RGBA{uint8(uint32(v.R) * (255 - alpha) / 255), uint8(uint32(v.G) * (255 - alpha) / 255), uint8(uint32(v.B) * (255 - alpha) / 255), 255})
		}
	}
	title = strings.TrimSpace(title)
	words := strings.Fields(title)
	lines := []string{}
	line := ""
	for _, word := range words {
		if len(line)+len(word) > 24 {
			lines = append(lines, line)
			line = word
		} else {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	for i, line := range lines {
		if i < 3 {
			posterText(out, line, 740+i*44, 3)
		}
	}
	posterText(out, footer, 865, 2)
	var dest bytes.Buffer
	if err := jpeg.Encode(&dest, out, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return dest.Bytes(), nil
}
