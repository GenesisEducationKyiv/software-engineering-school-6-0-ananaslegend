package confirmer_test

import (
	"testing"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
)

func TestConfirmURL(t *testing.T) {
	got := confirmer.ConfirmURL("https://reposeetory.com", "tok456")
	want := "https://reposeetory.com/api/confirm/tok456"
	if got != want {
		t.Fatalf("ConfirmURL = %q, want %q", got, want)
	}
}
