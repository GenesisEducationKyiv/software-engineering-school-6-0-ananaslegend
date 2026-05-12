package github

import "fmt"

// ReleaseURL returns the canonical GitHub release page URL.
func ReleaseURL(owner, name, tag string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, name, tag)
}
