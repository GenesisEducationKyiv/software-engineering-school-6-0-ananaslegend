package pages_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/subscription/http/pages"
)

func TestRenderer_Confirmed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Confirmed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Confirmed") {
		t.Error("body: expected 'Confirmed'")
	}
}

func TestRenderer_Unsubscribed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unsubscribed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unsubscribed") {
		t.Error("body: expected 'Unsubscribed'")
	}
}

func TestRenderer_Unavailable_404(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unavailable(w, http.StatusNotFound)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unavailable") {
		t.Error("body: expected 'Unavailable'")
	}
}

func TestRenderer_Unavailable_410(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unavailable(w, http.StatusGone)

	if w.Code != http.StatusGone {
		t.Errorf("status: want 410, got %d", w.Code)
	}
}

func TestRenderer_Oops(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Oops(w, "abc123/req-001")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "abc123/req-001") {
		t.Error("body: expected request ID in response")
	}
}

func TestRenderer_Landing(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Landing(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "reposeetory") {
		t.Error("body: expected 'reposeetory'")
	}
	if !strings.Contains(w.Body.String(), "Watch repository") {
		t.Error("body: expected 'Watch repository'")
	}
}

func TestRenderer_Subscribed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Subscribed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Check your inbox") {
		t.Error("body: expected 'Check your inbox'")
	}
	if !strings.Contains(w.Body.String(), "Subscribe more") {
		t.Error("body: expected 'Subscribe more'")
	}
}
