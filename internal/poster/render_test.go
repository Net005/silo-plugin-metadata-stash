package poster

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestContainPreservesBothEdges(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			v := color.RGBA{0, 200, 0, 255}
			if x < 80 {
				v = color.RGBA{255, 0, 0, 255}
			}
			if x > 720 {
				v = color.RGBA{0, 0, 255, 255}
			}
			src.SetRGBA(x, y, v)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, src)
	out, e := RenderScenePoster(b.Bytes(), "Test title", "Studio / 2026", "contain")
	if e != nil {
		t.Fatal(e)
	}
	im, e := jpeg.Decode(bytes.NewReader(out))
	if e != nil {
		t.Fatal(e)
	}
	if im.Bounds() != image.Rect(0, 0, 600, 900) {
		t.Fatal(im.Bounds())
	}
	r, _, _, _ := im.At(20, 380).RGBA()
	_, _, blue, _ := im.At(580, 380).RGBA()
	if r < 50000 || blue < 50000 {
		t.Fatal("full-frame mode clipped source edges")
	}
}
func TestMalformedImageRejected(t *testing.T) {
	if _, e := RenderScenePoster([]byte("bad"), "", "", "contain"); e == nil {
		t.Fatal("invalid source accepted")
	}
}
func TestPortraitArtworkHasNoAddedFooter(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			src.SetRGBA(x, y, color.RGBA{240, 30, 20, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, src)
	out, e := RenderScenePoster(b.Bytes(), "Extra footer must not appear", "", "contain")
	if e != nil {
		t.Fatal(e)
	}
	im, e := jpeg.Decode(bytes.NewReader(out))
	if e != nil {
		t.Fatal(e)
	}
	r, _, _, _ := im.At(300, 880).RGBA()
	if r < 50000 {
		t.Fatal("existing portrait was darkened or overlaid")
	}
}
