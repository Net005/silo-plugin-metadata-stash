package poster

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestJAVJacketShapesAndFrontPanel(t *testing.T) {
	for _, shape := range []struct {
		w, h int
		want bool
	}{{800, 538, true}, {800, 443, true}, {1920, 1080, true}, {1600, 900, true}, {1000, 1500, false}, {1280, 720, true}, {800, 600, false}, {1200, 500, false}} {
		im := image.NewRGBA(image.Rect(0, 0, shape.w, shape.h))
		for y := 0; y < shape.h; y++ {
			for x := 0; x < shape.w; x++ {
				c := color.RGBA{230, 0, 0, 255}
				if x >= shape.w/2 {
					c = color.RGBA{0, 200, 0, 255}
				}
				im.Set(x, y, c)
			}
		}
		// Add a back-panel barcode to the jacket fixture.
		for y := shape.h * 85 / 100; y < shape.h*93/100; y++ {
			for x := shape.w / 10; x < shape.w*3/10; x++ {
				c := color.White
				if (x/2)%2 == 0 {
					c = color.Black
				}
				im.Set(x, y, c)
			}
		}
		var raw bytes.Buffer
		png.Encode(&raw, im)
		out, ok, err := RenderJAVPoster(raw.Bytes())
		if err != nil || ok != shape.want {
			t.Fatalf("%dx%d: %v %v", shape.w, shape.h, ok, err)
		}
		if ok {
			p, _, e := image.Decode(bytes.NewReader(out))
			if e != nil || p.Bounds().Dx() != 1000 || p.Bounds().Dy() != 1500 {
				t.Fatal("wrong poster bounds", e)
			}
			r, g, _, _ := p.At(500, 750).RGBA()
			if r > g {
				t.Fatal("back panel selected")
			}
		}
	}
}

func TestWideOrdinaryScreenshotIsNotJacket(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 800, 450))
	for y := 0; y < 450; y++ {
		for x := 0; x < 800; x++ {
			im.Set(x, y, color.RGBA{uint8(x / 4), uint8(y / 2), 100, 255})
		}
	}
	var raw bytes.Buffer
	png.Encode(&raw, im)
	_, ok, err := RenderJAVPoster(raw.Bytes())
	if err != nil || ok {
		t.Fatal("ordinary screenshot sliced", err)
	}
}
