package personal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// maxInlineImages caps remote-image fetches per message (abuse bound).
	maxInlineImages = 3
	// maxRemoteImageBytes caps a single download (5MB).
	maxRemoteImageBytes = 5 * 1024 * 1024
	// remoteImageTimeout bounds DNS + download per URL.
	remoteImageTimeout = 15 * time.Second
)

// bareURLLineRe matches a line containing nothing but an http(s) URL — e.g.
// a QR/payment image link the model pasted for the user to open.
var bareURLLineRe = regexp.MustCompile(`(?m)^\s*(https?://\S+)\s*$`)

// ssrfCheck validates a URL before download. Variable (not const) so tests can
// substitute a permissive checker for httptest servers.
var ssrfCheck = checkRemoteURLPublic

// blockedRemoteHostnames mirrors the platform SSRF policy: never fetch from
// these, even if DNS would resolve them publicly.
var blockedRemoteHostnames = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
}

// checkRemoteURLPublic rejects non-http(s) schemes, blocked hostnames, and
// hosts resolving to private/loopback/link-local addresses.
func checkRemoteURLPublic(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("missing hostname")
	}
	if blockedRemoteHostnames[host] ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("blocked hostname: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isNonPublicIP(ip) {
			return fmt.Errorf("private IP not allowed: %s", host)
		}
		return nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && isNonPublicIP(ip) {
			return fmt.Errorf("hostname %s resolves to private IP %s", host, addr)
		}
	}
	return nil
}

// isNonPublicIP reports loopback, private, link-local, and reserved ranges.
// NOTE: resolve-then-check has a residual DNS TOCTOU; proportionate here
// because URLs come from the agent (not remote attackers), and downloads are
// size/time-capped and never executed.
func isNonPublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() {
		return true
	}
	for _, cidr := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"100.64.0.0/10", "198.18.0.0/15", "169.254.0.0/16",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// imageExtForMIME maps a sniffed image MIME to an uploadable extension.
// gif is deliberately excluded: the image send path only takes jpg/png/webp.
func imageExtForMIME(mime string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	}
	return "", false
}

// fetchRemoteImage downloads one URL to a temp file if it is a public image.
// Anything else (SSRF-blocked, non-image, oversize, slow) is an error and the
// caller keeps the original URL text as fallback.
func fetchRemoteImage(ctx context.Context, rawURL string) (string, error) {
	if err := ssrfCheck(rawURL); err != nil {
		return "", err
	}
	dlCtx, cancel := context.WithTimeout(ctx, remoteImageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "goclaw-zalo/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	// Sniff before trusting headers: read head, detect, then stream the rest.
	head := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	mime := http.DetectContentType(head)
	ext, ok := imageExtForMIME(mime)
	if !ok {
		return "", fmt.Errorf("not an uploadable image (detected %s)", mime)
	}
	f, err := os.CreateTemp(os.TempDir(), "zalo-remote-img-*"+ext)
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if _, err := f.Write(head); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	// +1 byte tripwire: anything beyond the cap aborts the download.
	written, err := io.Copy(f, io.LimitReader(resp.Body, maxRemoteImageBytes-int64(n)+1))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if written >= maxRemoteImageBytes-int64(n)+1 {
		os.Remove(tmp)
		return "", fmt.Errorf("image exceeds %d bytes", maxRemoteImageBytes)
	}
	return tmp, nil
}

// extractInlineImageURLs returns bare-URL lines (capped), preserving order.
func extractInlineImageURLs(text string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, m := range bareURLLineRe.FindAllStringSubmatch(text, -1) {
		u := m[1]
		if seen[u] || len(urls) >= maxInlineImages {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

// fetchInlineImages downloads bare image URLs found in text. It returns the
// text with successfully-fetched URL lines removed and the temp file paths.
// Failures keep the original URL text (user can still tap the link).
func fetchInlineImages(ctx context.Context, text string) (string, []string) {
	urls := extractInlineImageURLs(text)
	if len(urls) == 0 {
		return text, nil
	}
	var fetched []string
	for _, u := range urls {
		tmp, err := fetchRemoteImage(ctx, u)
		if err != nil {
			slog.Debug("zalo_personal: inline image fetch skipped",
				"url_host", hostOf(u), "error", err)
			continue
		}
		slog.Info("zalo_personal: inline image fetched", "url_host", hostOf(u))
		fetched = append(fetched, tmp)
		// Remove exactly the bare line carrying this URL.
		lines := strings.Split(text, "\n")
		kept := lines[:0]
		removed := false
		for _, ln := range lines {
			if !removed && strings.TrimSpace(ln) == u {
				removed = true
				continue
			}
			kept = append(kept, ln)
		}
		text = strings.Join(kept, "\n")
	}
	return strings.TrimSpace(text), fetched
}

func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Hostname()
	}
	return ""
}

// downloadedExt returns the temp file's extension (for logging/tests).
func downloadedExt(tmp string) string {
	return filepath.Ext(tmp)
}
