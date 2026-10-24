package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

var (
	placeholderOnce sync.Once
	placeholderData []byte
)

var questionMarkBitmap = [7]string{
	"01110",
	"10001",
	"00001",
	"00010",
	"00100",
	"00000",
	"00100",
}

func placeholderIcon() []byte {
	placeholderOnce.Do(func() {
		placeholderData = drawPlaceholder()
	})
	return placeholderData
}

func drawPlaceholder() []byte {
	const size = 100
	const scale = 8
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fg := color.RGBA{R: 150, G: 150, B: 150, A: 210}

	glyphW, glyphH := 5*scale, 7*scale
	offX, offY := (size-glyphW)/2, (size-glyphH)/2
	for row := 0; row < len(questionMarkBitmap); row++ {
		for col := 0; col < len(questionMarkBitmap[row]); col++ {
			if questionMarkBitmap[row][col] != '1' {
				continue
			}
			x0, y0 := offX+col*scale, offY+row*scale
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.Set(x0+dx, y0+dy, fg)
				}
			}
		}
	}

	var buf bytes.Buffer

	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
