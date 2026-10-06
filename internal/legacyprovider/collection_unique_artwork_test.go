package legacyprovider

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func solidPoster(c color.RGBA) []byte {
	im := image.NewRGBA(image.Rect(0, 0, 80, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 80; x++ {
			im.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}
func TestPosterComparisonIgnoresEncodingAndSmallColorChanges(t *testing.T) {
	a, _ := posterSignature(solidPoster(color.RGBA{90, 40, 20, 255}))
	b, _ := posterSignature(solidPoster(color.RGBA{92, 42, 22, 255}))
	c, _ := posterSignature(solidPoster(color.RGBA{10, 100, 220, 255}))
	if !samePoster(a, b) || samePoster(a, c) {
		t.Fatal("poster visual comparison failed")
	}
	if bytes.Equal(collectionTitleCard("1", "Watchlist"), collectionTitleCard("2", "Watchlist")) {
		t.Fatal("fallback covers identical")
	}
}
func TestUniquePosterAcrossCollectionsAndDifferentMemberIDs(t *testing.T) {
	red := solidPoster(color.RGBA{190, 20, 30, 255})
	blue := solidPoster(color.RGBA{10, 40, 190, 255})
	d, _ := pixelDigest(red)
	sig, _ := posterSignature(red)
	marker := map[string]json.RawMessage{}
	uploads := 0
	var uploaded []byte
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		switch r.URL.Path {
		case "/api/v2/admin/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{{ID: "other", PosterURL: server.URL + "/red", SourceConfig: map[string]json.RawMessage{uniquePosterKey: mustJSON(uniquePosterMarker{Policy: 1, MediaID: "different-id", SourceDigest: d, PosterURL: server.URL + "/red", Signature: sig})}}, {ID: "target", Title: "Target", PosterURL: server.URL + "/cached", SourceConfig: marker}}})
		case "/red":
			w.Write(red)
		case "/blue":
			w.Write(blue)
		case "/api/v2/admin/collections/target/poster":
			r.ParseMultipartForm(1 << 20)
			f, _, e := r.FormFile("image")
			if e != nil {
				t.Error(e)
				return
			}
			defer f.Close()
			var b bytes.Buffer
			b.ReadFrom(f)
			uploaded = b.Bytes()
			uploads++
		case "/api/v2/admin/collections/target":
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(siloCollection{ID: "target", PosterURL: server.URL + "/cached", SourceConfig: marker})
			} else {
				var p struct {
					SourceConfig map[string]json.RawMessage `json:"source_config"`
				}
				json.NewDecoder(r.Body).Decode(&p)
				marker = p.SourceConfig
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewSiloClient(server.URL, "key")
	choices := []CollectionArtwork{{MediaID: "first", PosterURL: server.URL + "/red"}, {MediaID: "second", PosterURL: server.URL + "/blue"}}
	for i := 0; i < 2; i++ {
		if err := client.SetUniqueCollectionPoster(t.Context(), "target", choices); err != nil {
			t.Fatal(err)
		}
	}
	if uploads != 1 || !bytes.Equal(uploaded, blue) {
		t.Fatalf("uploads %d; did not reserve distinct blue poster", uploads)
	}
	// Identical membership with no unused photos must receive a distinct card.
	if err := client.SetUniqueCollectionPoster(t.Context(), "target", choices[:1]); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(uploaded, red) || bytes.Equal(uploaded, blue) {
		t.Fatal("did not generate fallback")
	}
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestCollectionCompositionRetainsBothMemberImages(t *testing.T) {
	red := solidPoster(color.RGBA{190, 20, 30, 255})
	blue := solidPoster(color.RGBA{10, 40, 190, 255})
	data := collectionMemberCard("collection", "Stash | Your Top Rated", [][]byte{red, blue})
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if im.Bounds().Dx() != 500 || im.Bounds().Dy() != 750 {
		t.Fatal("not a portrait cover")
	}
	if color.RGBAModel.Convert(im.At(125, 275)).(color.RGBA) != (color.RGBA{190, 20, 30, 255}) || color.RGBAModel.Convert(im.At(355, 275)).(color.RGBA) != (color.RGBA{10, 40, 190, 255}) {
		t.Fatal("member images missing")
	}
	if !bytes.Equal(data, collectionMemberCard("collection", "Stash | Your Top Rated", [][]byte{red, blue})) {
		t.Fatal("unstable composition")
	}
}

func TestCollectionReservationSurvivesSignedURLExpiry(t *testing.T) {
	m := uniquePosterMarker{Policy: 1, PosterThumbhash: "stable"}
	if !posterMarkerMatches(m, siloCollection{PosterThumbhash: "stable", PosterURL: "https://example.invalid/expired"}) {
		t.Fatal("stable cached poster reservation lost")
	}
	if posterMarkerMatches(m, siloCollection{PosterThumbhash: "replaced"}) {
		t.Fatal("changed artwork kept stale reservation")
	}
}
