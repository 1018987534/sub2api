package handler

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func supportTestPDF(extra string) []byte {
	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	first := body.Len()
	fmt.Fprintf(&body, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R %s >>\nendobj\n", extra)
	second := body.Len()
	body.WriteString("2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", first, second, xref)
	return body.Bytes()
}

func TestSupportAttachmentContentValidatesImagesAndDocuments(t *testing.T) {
	var pngData, jpegData, gifData bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	require.NoError(t, png.Encode(&pngData, im))
	require.NoError(t, jpeg.Encode(&jpegData, im, nil))
	require.NoError(t, gif.Encode(&gifData, image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.Black}), nil))
	for _, input := range []struct {
		name     string
		data     []byte
		accepted bool
	}{
		{"good.png", pngData.Bytes(), true},
		{"good.jpg", jpegData.Bytes(), true},
		{"good.gif", gifData.Bytes(), true},
		{"notes.txt", []byte("正常客服信息\n"), true},
		{"good.pdf", supportTestPDF(""), true},
		{"header.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}, false},
		{"poly.png", append(append([]byte(nil), pngData.Bytes()...), []byte("<html><script>alert(1)</script></html>")...), false},
		{"poly.jpg", append(append([]byte(nil), jpegData.Bytes()...), []byte("<html>x</html>")...), false},
		{"header.pdf", []byte("%PDF-1.4\n<html>x</html>\n%%EOF\n"), false},
		{"active.pdf", supportTestPDF("/OpenAction << /S /JavaScript /JS (alert(1)) >>"), false},
		{"escaped.pdf", supportTestPDF("/Open#41ction << /S /Java#53cript /JS (alert(1)) >>"), false},
	} {
		t.Run(input.name, func(t *testing.T) {
			_, ok := detectSupportAttachmentType(input.name, input.data)
			require.Equal(t, input.accepted, ok)
		})
	}
}

func TestSupportAttachmentHistoricalHTMLIsForcedToDownload(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`SELECT a.filename,a.content_type,a.data`).WithArgs(int64(4)).WillReturnRows(sqlmock.NewRows([]string{"filename", "content_type", "data"}).AddRow("probe.html", "text/html", []byte("<script>alert(1)</script>")))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "4"}}
	c.Request = httptest.NewRequest("GET", "/admin/chat/attachments/4", nil)
	NewSupportChatHandler(db, nil).AdminAttachment(c)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSupportChatMessageKindIsOwnedByServer(t *testing.T) {
	for _, kind := range []string{"html", "markdown", "admin", "system"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery(`INSERT INTO support_messages`).WithArgs(int64(41), "user", int64(7), "hello", "text", "kind-key").WillReturnRows(supportMessageRows(101, 41, 7, "user", "hello", time.Now()))
			mock.ExpectExec(`UPDATE support_conversations`).WillReturnResult(sqlmock.NewResult(0, 1))
			_, err = NewSupportChatHandler(db, nil).insertMessage(supportChatTestContext(), 41, 7, "user", supportMessageInput{Content: "hello", Kind: kind, IdempotencyKey: "kind-key"})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
