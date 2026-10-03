//go:build integration || e2e

package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// Browser is the shared Playwright + Chromium handle for an e2e suite.
// Build one in SetupSuite; create a fresh Page per test method.
type Browser struct {
	pw      *playwright.Playwright
	browser playwright.Browser
}

// NewBrowser launches Chromium and registers t.Cleanup to tear it down.
// Headless by default; set PLAYWRIGHT_HEADED=1 to flip it for debugging.
func NewBrowser(t testing.TB) *Browser {
	t.Helper()
	pw, err := playwright.Run()
	require.NoError(t, err, "playwright run")

	headless := os.Getenv("PLAYWRIGHT_HEADED") != "1"
	br, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	})
	require.NoError(t, err, "chromium launch")

	b := &Browser{pw: pw, browser: br}
	t.Cleanup(func() {
		_ = br.Close()
		_ = pw.Stop()
	})
	return b
}

// NewPage opens an isolated BrowserContext + Page and registers cleanup
// that, on t.Failed(), saves a screenshot to tests/e2e/_artifacts/.
func (b *Browser) NewPage(t testing.TB) playwright.Page {
	t.Helper()
	ctx, err := b.browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: 1280, Height: 720},
	})
	require.NoError(t, err, "new browser context")

	page, err := ctx.NewPage()
	require.NoError(t, err, "new page")

	t.Cleanup(func() {
		if t.Failed() {
			saveScreenshot(t, page)
		}
		_ = ctx.Close()
	})
	return page
}

// LoadEmailHTML opens an isolated context and renders html via SetContent so
// callers can reuse the same data-testid selectors as on real pages.
func (b *Browser) LoadEmailHTML(t testing.TB, html string) playwright.Page {
	t.Helper()
	ctx, err := b.browser.NewContext()
	require.NoError(t, err, "new browser context for email")

	page, err := ctx.NewPage()
	require.NoError(t, err, "new page for email")

	require.NoError(t, page.SetContent(html), "page.SetContent")

	t.Cleanup(func() { _ = ctx.Close() })
	return page
}

func saveScreenshot(t testing.TB, page playwright.Page) {
	t.Helper()
	dir := filepath.Join("..", "_artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("screenshot: mkdir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, sanitize(t.Name())+".png")
	_, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(path),
		FullPage: playwright.Bool(true),
	})
	if err != nil {
		t.Logf("screenshot: %v", err)
		return
	}
	t.Logf("screenshot saved to %s", path)
}

func sanitize(name string) string {
	r := strings.NewReplacer("/", "_", " ", "_", ":", "_")
	return r.Replace(name)
}
