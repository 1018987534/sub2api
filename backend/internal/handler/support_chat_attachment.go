package handler

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	_ "golang.org/x/image/webp"
)

const supportChatMaxImagePixels = 16 << 20

var supportPDFStartXRef = regexp.MustCompile(`startxref\s+([0-9]+)\s+%%EOF\s*$`)
var supportPDFCatalog = regexp.MustCompile(`/Type\s*/Catalog\b`)
var supportPDFRoot = regexp.MustCompile(`/Root\s+[0-9]+\s+[0-9]+\s+R\b`)
var supportPDFActiveContent = regexp.MustCompile(`/(?:JavaScript|JS|Launch|OpenAction|AA|EmbeddedFile|RichMedia)\b`)
var supportPDFNameEscape = regexp.MustCompile(`#([0-9A-Fa-f]{2})`)

func validateSupportAttachmentContent(contentType string, data []byte) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > supportChatMaxImagePixels {
			return false
		}
		reader := bytes.NewReader(data)
		if _, _, err := image.Decode(reader); err != nil {
			return false
		}
		switch format {
		case "png":
			// image.Decode may read ahead; inspect chunk boundaries independently
			// so buffered reads cannot hide a trailing HTML payload.
			return supportPNGEndsAtIEND(data)
		case "jpeg":
			return bytes.HasSuffix(data, []byte{0xff, 0xd9})
		case "gif":
			return data[len(data)-1] == 0x3b
		case "webp":
			return len(data) >= 12 && int64(binary.LittleEndian.Uint32(data[4:8]))+8 == int64(len(data))
		}
		return false
	case "application/pdf":
		return validateSupportPDF(data)
	case "text/plain":
		return utf8.Valid(data) && !bytes.ContainsRune(data, 0)
	default:
		return false
	}
}

func supportPNGEndsAtIEND(data []byte) bool {
	for offset := 8; offset+12 <= len(data); {
		length := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length > int64(len(data)-offset-12) {
			return false
		}
		next := offset + int(length) + 12
		if string(data[offset+4:offset+8]) == "IEND" {
			return length == 0 && next == len(data)
		}
		offset = next
	}
	return false
}

// Check PDF document structure instead of accepting a header-only payload.
// Downloads remain isolated; this is not an antivirus or a PDF execution engine.
func validateSupportPDF(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("%PDF-")) || len(data) < 40 {
		return false
	}
	match := supportPDFStartXRef.FindSubmatch(data)
	if len(match) != 2 {
		return false
	}
	offset, err := strconv.ParseInt(string(match[1]), 10, 64)
	if err != nil || offset < 8 || offset >= int64(len(data)) {
		return false
	}
	section := bytes.TrimSpace(data[offset:])
	if !bytes.HasPrefix(section, []byte("xref")) && !bytes.Contains(section, []byte("/XRef")) {
		return false
	}
	// PDF names can encode letters as #XX; decode those before checking actions.
	names := supportPDFNameEscape.ReplaceAllStringFunc(string(data), func(encoded string) string {
		value, _ := strconv.ParseUint(encoded[1:], 16, 8)
		return string([]byte{byte(value)})
	})
	return (supportPDFCatalog.MatchString(names) || supportPDFRoot.MatchString(names)) && !supportPDFActiveContent.MatchString(names) && !strings.Contains(names, "<script")
}
