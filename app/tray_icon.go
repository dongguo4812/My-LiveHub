package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

func createTrayIcon() []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	background := color.NRGBA{R: 20, G: 25, B: 38, A: 255}
	border := color.NRGBA{R: 65, G: 115, B: 190, A: 255}
	accent := color.NRGBA{R: 91, G: 220, B: 225, A: 255}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			distance := dx*dx + dy*dy
			if distance <= 14.5*14.5 {
				pixel := background
				if distance >= 12.8*12.8 {
					pixel = border
				}
				img.SetNRGBA(x, y, pixel)
			}
		}
	}

	// Four cyan bars form a compact live-audio waveform mark.
	heights := [...]int{7, 13, 10, 6}
	for i, height := range heights {
		x := 8 + i*4
		top := (size - height) / 2
		for y := top; y < top+height; y++ {
			for px := x; px < x+2; px++ {
				img.SetNRGBA(px, y, accent)
			}
		}
	}

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return nil
	}
	return buffer.Bytes()
}
