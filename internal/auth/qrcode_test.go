package auth

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"rsc.io/qr"
)

// TestTheQRCodeDrawsEveryModuleItEncodes reads the drawing back and holds it
// to the encoder's own grid, module for module, with the quiet zone around it.
// A drawing that drops one row, or is off by one, is a square no phone reads,
// and nothing else in the panel would notice.
func TestTheQRCodeDrawsEveryModuleItEncodes(t *testing.T) {
	uri := TOTPURI("Skifity", "someone@example.test", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")
	svg, err := TOTPQRCode(uri)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	want, err := qr.Encode(uri, qr.M)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	side := want.Size + 2*qrQuietZone

	if !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg"`) {
		t.Fatalf("not an SVG document: %.60s", svg)
	}
	if !strings.Contains(svg, fmt.Sprintf(`viewBox="0 0 %d %d"`, side, side)) {
		t.Errorf("the view box is not the code plus its quiet zone (%d): %.120s", side, svg)
	}

	drawn := make([][]bool, side)
	for y := range drawn {
		drawn[y] = make([]bool, side)
	}
	run := regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)
	path := regexp.MustCompile(`d="([^"]*)"`).FindStringSubmatch(svg)
	if path == nil {
		t.Fatal("the drawing has no path")
	}
	if leftover := run.ReplaceAllString(path[1], ""); leftover != "" {
		t.Fatalf("the path has something in it that is not a run of modules: %q", leftover)
	}
	for _, m := range run.FindAllStringSubmatch(path[1], -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		n, _ := strconv.Atoi(m[3])
		if m[3] != m[4] {
			t.Fatalf("a run does not close on itself: %s", m[0])
		}
		for i := range n {
			if x+i >= side || y >= side {
				t.Fatalf("a run is drawn outside the code: %s", m[0])
			}
			drawn[y][x+i] = true
		}
	}

	for y := range side {
		for x := range side {
			inside := x >= qrQuietZone && y >= qrQuietZone && x < side-qrQuietZone && y < side-qrQuietZone
			expected := inside && want.Black(x-qrQuietZone, y-qrQuietZone)
			if drawn[y][x] != expected {
				t.Fatalf("module (%d, %d) is drawn %v, and the encoder says %v", x, y, drawn[y][x], expected)
			}
		}
	}
}
