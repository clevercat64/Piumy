package main

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// uniformTestIcon is a solid, saturated square — saturated so a hue
// rotation actually changes it (a grey icon would rotate to itself and
// make the "tint preserved" assertions vacuous).
func uniformTestIcon(side int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func countMarkPixels(t *testing.T, img image.Image) (red int, outsideBottomRight int) {
	t.Helper()
	b := img.Bounds()
	midX := b.Min.X + b.Dx()/2
	midY := b.Min.Y + b.Dy()/2
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) != alertRed {
				continue
			}
			red++
			if x < midX || y < midY {
				outsideBottomRight++
			}
		}
	}
	return red, outsideBottomRight
}

// TestAlertMarkIsPaintedForTheDefaultAccount is the test Citrino asked for
// by name, and it guards a trap that is easy to walk back into:
// RecolorTrayIcon returns the container UNTOUCHED when hueDelta == 0, which
// is exactly the default install (config.ColorForAccount("").HueDelta == 0).
// If the alert mark is ever hung off that shortcut, the most common install
// loses its alert entirely — silently, with build, vet and every other test
// still green. This test is the only thing that says so out loud.
func TestAlertMarkIsPaintedForTheDefaultAccount(t *testing.T) {
	src := uniformTestIcon(32, color.NRGBA{R: 0x20, G: 0x80, B: 0xC0, A: 0xFF})
	ico := makeTestICO(t, src)

	got, err := AlertTrayIcon(ico, 0) // 0 == no account == the default install
	if err != nil {
		t.Fatalf("AlertTrayIcon: %v", err)
	}
	if bytes.Equal(got, ico) {
		t.Fatal("la cuenta POR DEFECTO (hueDelta 0) quedó sin marca de alerta: el contenedor volvió intacto")
	}

	red, outside := countMarkPixels(t, decodeICOFirstImage(t, got))
	if red == 0 {
		t.Fatal("no se pintó ni un píxel de la marca con hueDelta 0")
	}
	if outside != 0 {
		t.Errorf("la marca debe quedar en el cuadrante inferior derecho; %d píxeles cayeron fuera", outside)
	}
}

// The account tint must survive the mark: that is the boss's rule (the
// account's visual identity goes everywhere) and the reason the mark is
// painted ON TOP instead of replacing the hue.
func TestAlertMarkKeepsTheAccountTint(t *testing.T) {
	const delta = 90.0
	src := uniformTestIcon(32, color.NRGBA{R: 0x20, G: 0x80, B: 0xC0, A: 0xFF})
	ico := makeTestICO(t, src)

	tinted, err := RecolorTrayIcon(ico, delta)
	if err != nil {
		t.Fatalf("RecolorTrayIcon: %v", err)
	}
	alerted, err := AlertTrayIcon(ico, delta)
	if err != nil {
		t.Fatalf("AlertTrayIcon: %v", err)
	}

	tintedImg := decodeICOFirstImage(t, tinted)
	alertedImg := decodeICOFirstImage(t, alerted)

	// Top-left is far from the bottom-right mark: there the alerted icon
	// must be pixel-identical to the merely tinted one.
	at := func(img image.Image, x, y int) color.NRGBA {
		return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	}
	if got, want := at(alertedImg, 2, 2), at(tintedImg, 2, 2); got != want {
		t.Errorf("la marca alteró el color de cuenta lejos de ella: %+v, esperado %+v", got, want)
	}
	// Control of this assertion: the tint must really have changed the
	// pixel, or comparing the two above would prove nothing.
	if at(tintedImg, 2, 2) == color.NRGBA(src.NRGBAAt(2, 2)) {
		t.Fatal("control: el tinte no cambió el píxel, así que la comparación anterior no prueba nada")
	}
	// And the mark itself must be there.
	if red, _ := countMarkPixels(t, alertedImg); red == 0 {
		t.Error("con tinte de cuenta, la marca no se pintó")
	}
}

// A broken container must leave the caller with the icon it had, same
// contract RecolorTrayIcon already honors — the tray must never end up
// with no icon because an alert could not be painted.
func TestAlertTrayIconMalformedContainerFallsBack(t *testing.T) {
	src := []byte{1, 2, 3, 4, 5} // not a valid ICO
	got, err := AlertTrayIcon(src, 0)
	if err == nil {
		t.Error("un contenedor inválido debe devolver error")
	}
	if !bytes.Equal(got, src) {
		t.Errorf("debe devolver la entrada intacta; devolvió %v", got)
	}
}
