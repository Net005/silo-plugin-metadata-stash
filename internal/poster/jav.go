package poster

import (
	"bytes"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	"math"
)

// RenderJAVPoster is called only for a verified JAV scene code. Recognise DVD
// and wider Blu-ray jacket spreads; never slice an ordinary scene screenshot.
// Keep the right front panel in full and fit it to 2:3 without another crop.
func RenderJAVPoster(raw []byte) ([]byte, bool, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 30000000 {
		return nil, false, nil
	}
	ratio := float64(cfg.Width) / float64(cfg.Height)
	if !((ratio >= 1.39 && ratio <= 1.59) || (ratio >= 1.72 && ratio <= 1.91)) {
		return nil, false, nil
	}
	im, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	b := im.Bounds()
	frontWidth := int(math.Round(float64(b.Dx()) * 0.474))
	seam := jacketSeam(im)
	barcode := jacketBarcode(im)
	if ratio >= 1.72 && !barcode || seam == 0 && !barcode {
		return nil, false, nil
	}
	if seam != 0 {
		frontWidth = b.Max.X - seam
	}

	front := posterCrop(im, image.Rect(b.Max.X-frontWidth, b.Min.Y, b.Max.X, b.Max.Y))
	const w, h = 1000, 1500
	scale := math.Min(float64(w)/float64(frontWidth), float64(h)/float64(b.Dy()))
	fw, fh := max(1, int(math.Round(float64(frontWidth)*scale))), max(1, int(math.Round(float64(b.Dy())*scale)))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	stdraw.Draw(out, out.Bounds(), &image.Uniform{C: color.RGBA{18, 18, 20, 255}}, image.Point{}, stdraw.Src)
	fitted := posterResize(front, fw, fh)
	at := image.Pt((w-fw)/2, (h-fh)/2)
	stdraw.Draw(out, image.Rectangle{Min: at, Max: at.Add(fitted.Bounds().Size())}, fitted, image.Point{}, stdraw.Src)
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: 96})
	return buf.Bytes(), err == nil, err
}

// Wide covers overlap screenshot aspect ratios. Require a sustained jacket
// seam near the centre before selecting a front panel from these shapes.
func jacketSeam(im image.Image) int {
	b := im.Bounds()
	best, bestStrength := 0, 0.0
	step := max(1, b.Dy()/100)
	offset := max(1, b.Dx()/250)
	for x := b.Min.X + b.Dx()*45/100; x < b.Min.X+b.Dx()*56/100; x++ {
		total, strong := 0, 0
		sum := 0.0
		for y := b.Min.Y + b.Dy()/20; y < b.Max.Y-b.Dy()/20; y += step {
			r1, g1, b1, _ := im.At(x-offset, y).RGBA()
			r2, g2, b2, _ := im.At(x+offset, y).RGBA()
			delta := (math.Abs(float64(r1)-float64(r2)) + math.Abs(float64(g1)-float64(g2)) + math.Abs(float64(b1)-float64(b2))) / 771
			total++
			sum += delta
			if delta > 35 {
				strong++
			}
		}
		strength := sum / float64(max(1, total))
		if strong*100 >= total*60 && strength > bestStrength {
			best, bestStrength = x, strength
		}
	}
	return best
}

// A jacket back panel has repeated black/white barcode edges near its bottom.
// This second signal distinguishes wider jackets from 16:9 scene photographs.
func jacketBarcode(im image.Image) bool {
	b := im.Bounds()
	span := max(30, b.Dx()/5)
	dy := max(3, b.Dy()/50)
	edges := func(y, start int) []int {
		result := []int{}
		prev := -1
		for x := start; x < start+span; x++ {
			r, g, bl, _ := im.At(x, y).RGBA()
			v := int((r + g + bl) / 771)
			binary := -1
			if v < 65 {
				binary = 0
			} else if v > 185 {
				binary = 1
			}
			if binary < 0 {
				continue
			}
			if prev >= 0 && prev != binary {
				result = append(result, x)
			}
			prev = binary
		}
		return result
	}
	for y := b.Min.Y + b.Dy()*65/100; y < b.Max.Y-dy; y += max(1, b.Dy()/100) {
		for x := b.Min.X; x+span < b.Min.X+b.Dx()*48/100; x += max(1, span/5) {
			a, c := edges(y, x), edges(y+dy, x)
			if len(a) < 12 || len(c) < 12 {
				continue
			}
			matched := 0
			for _, edge := range a {
				for _, other := range c {
					if int(math.Abs(float64(edge-other))) <= 1 {
						matched++
						break
					}
				}
			}
			if matched*100 >= len(a)*80 {
				return true
			}
		}
	}
	return false
}
