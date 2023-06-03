package main

import (
	"fmt"
	"image"

	"github.com/muesli/smartcrop"
	"github.com/muesli/smartcrop/nfnt"
	"github.com/nfnt/resize"
)

func GetBestPieceOfImage(width, height int, img image.Image) image.Image {
	analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	bestCrop, _ := analyzer.FindBestCrop(img, 800, 480)

	fmt.Printf("Best crop: %+v\n", bestCrop)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedImg := img.(SubImager).SubImage(bestCrop)

	resizedImg := resize.Resize(800, 480, croppedImg, resize.Lanczos3)

	return resizedImg
}
