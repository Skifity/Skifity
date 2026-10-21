package auth

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// qrQuietZone is the blank border, in modules, that a scanner needs to find
// the code's edges. The standard asks for four.
const qrQuietZone = 4

// TOTPQRCode draws an otpauth URI as a QR code, as an SVG document.
//
// The setup screen told people to scan something and showed them a 32-letter
// secret to type into a phone instead. The code is drawn here, with rsc.io/qr
// — three small packages and no dependencies of their own — rather than by an
// encoder shipped to every browser, and an SVG is a few kilobytes that need
// nothing to display.
//
// Black on white whatever the panel's theme, with the quiet zone included:
// an authenticator app reads dark modules on a light ground, and a code drawn
// light on dark, or cropped to its modules, is one some phones never find.
func TOTPQRCode(uri string) (string, error) {
	// M recovers from about fifteen per cent of the code being unreadable,
	// which covers glare on a laptop screen, and keeps an otpauth URI small
	// enough to read at arm's length.
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return "", fmt.Errorf("draw the two-factor QR code: %w", err)
	}
	side := code.Size + 2*qrQuietZone

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, side, side)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/>`, side, side)
	b.WriteString(`<path fill="#000" d="`)
	for y := range code.Size {
		// One rectangle per run of dark modules rather than one per module,
		// which keeps the document a third of the size.
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+qrQuietZone, y+qrQuietZone, run, run)
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
