package service

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompressOpenAIImagesMultipartRequestResizesAndReencodes(t *testing.T) {
	var source bytes.Buffer
	writer := multipart.NewWriter(&source)
	part, err := writer.CreateFormFile("image", "source.png")
	require.NoError(t, err)
	original := image.NewRGBA(image.Rect(0, 0, 3000, 1500))
	for y := 0; y < 1500; y += 10 {
		for x := 0; x < 3000; x += 10 {
			original.SetRGBA(x, y, color.RGBA{R: 220, G: 80, B: 40, A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, original, &jpeg.Options{Quality: 95}))
	_, err = part.Write(encoded.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("prompt", "resize"))
	require.NoError(t, writer.Close())

	compressed, contentType, err := CompressOpenAIImagesMultipartRequest(source.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	require.NotEqual(t, source.Bytes(), compressed)
	require.Contains(t, contentType, "multipart/form-data")

	reader := multipart.NewReader(bytes.NewReader(compressed), multipartBoundary(t, contentType))
	imagePart, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", imagePart.Header.Get("Content-Type"))
	config, _, err := image.DecodeConfig(imagePart)
	require.NoError(t, err)
	require.Equal(t, 2048, config.Width)
	require.Equal(t, 1024, config.Height)
}

func TestCompressOpenAIImagesMultipartRequestKeepsTransparencyAndMasksPNG(t *testing.T) {
	var source bytes.Buffer
	writer := multipart.NewWriter(&source)
	transparent := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	transparent.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 80})
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, transparent))
	part, err := writer.CreateFormFile("image", "transparent.jpg")
	require.NoError(t, err)
	_, err = part.Write(imageBytes.Bytes())
	require.NoError(t, err)
	mask := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var maskBytes bytes.Buffer
	require.NoError(t, png.Encode(&maskBytes, mask))
	part, err = writer.CreateFormFile("mask", "mask.jpg")
	require.NoError(t, err)
	_, err = part.Write(maskBytes.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	compressed, contentType, err := CompressOpenAIImagesMultipartRequest(source.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(compressed), multipartBoundary(t, contentType))
	for i := 0; i < 2; i++ {
		imagePart, err := reader.NextPart()
		require.NoError(t, err)
		require.Equal(t, "image/png", imagePart.Header.Get("Content-Type"))
	}
}

func TestCompressOpenAIImagesMultipartRequestRejectsOversizedAndNonImageParts(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{name: "oversized", data: bytes.Repeat([]byte("x"), int(OpenAIImagesMaxUploadPartBytes)+1), want: "exceeds 10 MB"},
		{name: "invalid image", data: []byte("not an image"), want: "decode image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source bytes.Buffer
			writer := multipart.NewWriter(&source)
			part, err := writer.CreateFormFile("image", "source.png")
			require.NoError(t, err)
			_, err = part.Write(tc.data)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			_, _, err = CompressOpenAIImagesMultipartRequest(source.Bytes(), writer.FormDataContentType())
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func multipartBoundary(t *testing.T, contentType string) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	return strings.TrimSpace(params["boundary"])
}
