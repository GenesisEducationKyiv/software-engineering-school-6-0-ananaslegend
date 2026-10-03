package notifier_test

import (
	"testing"

	"github.com/ananaslegend/reposeetory/internal/notifier"
)

func TestUnsubscribeURL(t *testing.T) {
	got := notifier.UnsubscribeURL("http://localhost:8080", "tok123")
	want := "http://localhost:8080/api/unsubscribe/tok123"
	if got != want {
		t.Fatalf("UnsubscribeURL = %q, want %q", got, want)
	}
}
