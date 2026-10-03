package github_test

import (
	"testing"

	"github.com/ananaslegend/reposeetory/internal/github"
)

func TestReleaseURL(t *testing.T) {
	got := github.ReleaseURL("ananaslegend", "reposeetory", "v1.2.3")
	want := "https://github.com/ananaslegend/reposeetory/releases/tag/v1.2.3"
	if got != want {
		t.Fatalf("ReleaseURL = %q, want %q", got, want)
	}
}
