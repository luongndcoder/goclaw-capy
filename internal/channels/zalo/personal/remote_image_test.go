package personal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCheckRemoteURLPublic_Blocks(t *testing.T) {
	blocked := []string{
		"http://localhost/x.png",
		"http://LOCALHOST:8080/x.png",
		"http://metadata.google.internal/x",
		"http://127.0.0.1/x.png",
		"http://192.168.1.100/x.png",
		"http://10.0.0.5/x.png",
		"http://169.254.169.254/latest",
		"ftp://example.com/x.png",
		"http:///missing-host",
	}
	for _, u := range blocked {
		if err := checkRemoteURLPublic(u); err == nil {
			t.Errorf("must block %q", u)
		}
	}
}

func TestExtractInlineImageURLs(t *testing.T) {
	text := "Quét mã này nhé:\nhttps://cdn.example.com/qr/abc.jpg\nCảm ơn!"
	urls := extractInlineImageURLs(text)
	if len(urls) != 1 || urls[0] != "https://cdn.example.com/qr/abc.jpg" {
		t.Fatalf("got %v", urls)
	}
	// Inline (non-bare) URLs are not fetched.
	if urls := extractInlineImageURLs("xem https://cdn.example.com/a.jpg giúp em"); len(urls) != 0 {
		t.Fatalf("inline URL must not match, got %v", urls)
	}
}

func TestImageExtForMIME(t *testing.T) {
	for mime, want := range map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp",
	} {
		if ext, ok := imageExtForMIME(mime); !ok || ext != want {
			t.Errorf("mime %q -> %q,%v want %q,true", mime, ext, ok, want)
		}
	}
	for _, mime := range []string{"image/gif", "text/html", "application/pdf", ""} {
		if _, ok := imageExtForMIME(mime); ok {
			t.Errorf("mime %q must be rejected", mime)
		}
	}
}

// TestFetchInlineImages_EndToEnd serves a 1x1 PNG over httptest with a
// permissive SSRF checker (loopback is blocked by the real one).
func TestFetchInlineImages_EndToEnd(t *testing.T) {
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xff, 0xff, 0x3f,
		0x00, 0x05, 0xfe, 0x02, 0xfe, 0xdc, 0xcc, 0x59,
		0xe7, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream") // wrong header: sniff must win
		w.Write(png)
	}))
	defer srv.Close()

	old := ssrfCheck
	ssrfCheck = func(string) error { return nil }
	defer func() { ssrfCheck = old }()

	text := fmt.Sprintf("Quét mã này:\n%s/qr.png\nCảm ơn!", srv.URL)
	rest, fetched := fetchInlineImages(context.Background(), text)
	if len(fetched) != 1 {
		t.Fatalf("want 1 fetched file, got %d", len(fetched))
	}
	defer os.Remove(fetched[0])
	if downloadedExt(fetched[0]) != ".png" {
		t.Errorf("want .png temp file, got %s", fetched[0])
	}
	if strings.Contains(rest, "/qr.png") {
		t.Errorf("fetched URL line must be removed, rest:\n%s", rest)
	}
	if !strings.Contains(rest, "Quét mã này:") || !strings.Contains(rest, "Cảm ơn!") {
		t.Errorf("surrounding text must survive, rest:\n%s", rest)
	}
}

// TestFetchInlineImages_FailureKeepsURL: non-image content keeps link text.
func TestFetchInlineImages_FailureKeepsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>not an image</html>")
	}))
	defer srv.Close()

	old := ssrfCheck
	ssrfCheck = func(string) error { return nil }
	defer func() { ssrfCheck = old }()

	text := fmt.Sprintf("Xem link:\n%s/page\nHết.", srv.URL)
	rest, fetched := fetchInlineImages(context.Background(), text)
	if len(fetched) != 0 {
		t.Fatalf("non-image must not fetch, got %v", fetched)
	}
	if !strings.Contains(rest, "/page") {
		t.Errorf("failed URL must stay as tappable text, rest:\n%s", rest)
	}
}
