package main

// Paints the "WhatsApp is down" mark ON TOP of the account-colored tray
// icon (T190, ct-2026-11-11-101756). Its own file, no build tag, same as
// trayicon_recolor.go: pure image arithmetic, so its test runs everywhere
// even though only tray_windows.go calls it.
//
// WHY ON TOP and not instead of the color: the account tint is the install's
// visual identity and it goes everywhere — dropping it from one surface is
// a defect, not a trade-off (boss's rule, relayed by Citrino 2026-11-11:
// "para mí es obvio"). With two Piumys open, the owner has to be able to
// see WHICH account went down, so the icon has to carry both facts at once.
//
// THE TRAP THIS FILE EXISTS TO AVOID: RecolorTrayIcon returns early when
// hueDelta == 0 — the default install's case, where there is no account
// tint to apply. Hanging the mark off that function would have left the
// MOST COMMON install with no alert mark at all, silently, while passing
// build, vet and every existing test. AlertTrayIcon therefore has no
// shortcut: the mark is unconditional, and
// TestAlertMarkIsPaintedForTheDefaultAccount fails if that ever regresses.

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// alertMarkFraction is the mark's diameter as a fraction of the icon's
// shorter side, so it scales with each image inside the ICO (the shipped
// container holds several sizes) instead of being a pixel count that is
// invisible at 48px and covers the whole glyph at 16px.
const alertMarkFraction = 0.42

// alertMarkMinDiameter keeps the mark legible on the smallest embedded
// size: below ~3px a dot stops reading as a dot.
const alertMarkMinDiameter = 3

// alertRed / alertRing: the mark's own colors, defined here rather than
// taken from a theme on purpose — the tray lives in the OS shell, which
// has no app theme, and this red is the SEMANTIC "something is wrong", not
// a brand color (the brand's color is the hue rotation underneath, which
// this mark deliberately leaves alone). The dark ring keeps the dot
// visible where the icon itself is red-ish, which a bare dot would not be.
var (
	alertRed  = color.NRGBA{R: 0xD9, G: 0x24, B: 0x2B, A: 0xFF}
	alertRing = color.NRGBA{R: 0x20, G: 0x10, B: 0x10, A: 0xFF}
)

// AlertTrayIcon returns icoData with the account tint applied (hueDelta, 0
// meaning "no account, leave the hue alone") AND the alert mark painted on
// top. Any failure returns icoData unchanged plus a non-nil error, same
// contract as RecolorTrayIcon: the caller logs it and keeps the icon it
// had — a broken mark must never leave the tray with no icon.
func AlertTrayIcon(icoData []byte, hueDelta float64) ([]byte, error) {
	return transformTrayIcon(icoData, func(img image.Image) *image.NRGBA {
		var out *image.NRGBA
		if hueDelta == 0 {
			// Not rotateHue(img, 0): that would still round every pixel
			// through RGB->HSV->RGB for no reason. A plain copy is both
			// exact and cheaper.
			out = copyToNRGBA(img)
		} else {
			out = rotateHue(img, hueDelta)
		}
		drawAlertMark(out)
		return out
	})
}

// copyToNRGBA converts any image into an editable NRGBA copy. Always a
// copy, never the source buffer aliased: the caller paints over the result
// and the decoded image must stay untouched.
func copyToNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	return out
}

// drawAlertMark paints a filled dot with a one-pixel darker ring in the
// bottom-right corner, fully opaque so it reads over a transparent or a
// busy icon alike.
func drawAlertMark(img *image.NRGBA) {
	b := img.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	if side < alertMarkMinDiameter {
		return // nothing sane to draw on an icon this small
	}

	diameter := int(math.Round(float64(side) * alertMarkFraction))
	if diameter < alertMarkMinDiameter {
		diameter = alertMarkMinDiameter
	}
	if diameter > side {
		diameter = side
	}
	radius := float64(diameter) / 2

	// Sit the dot flush in the bottom-right corner: the glyph's own detail
	// is centered, so a corner keeps the mark from hiding it.
	cx := float64(b.Max.X) - radius
	cy := float64(b.Max.Y) - radius

	// The ring is one pixel thick, except on a tiny icon where that would
	// eat the whole dot — there it takes a fixed share instead.
	ringOuter := radius
	ringInner := math.Max(radius-1, radius*0.6)

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// +0.5: measure from the pixel's center, so the circle is not
			// lopsided by half a pixel.
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			dist := math.Sqrt(dx*dx + dy*dy)
			switch {
			case dist <= ringInner:
				img.SetNRGBA(x, y, alertRed)
			case dist <= ringOuter:
				img.SetNRGBA(x, y, alertRing)
			}
		}
	}
}
