package domain

import (
	"regexp"
	"strings"
)

// RepoRef identifies a GitHub repository by its "owner/name" pair.
type RepoRef struct {
	Owner string
	Name  string
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// ParseRepoRef accepts a plain "owner/name" or a GitHub URL form
// (with optional ".git" suffix or trailing slash) and returns a validated RepoRef.
func ParseRepoRef(s string) (RepoRef, error) {
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		s = parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	if !repoNameRe.MatchString(s) {
		return RepoRef{}, ErrInvalidRepoFormat
	}
	i := strings.IndexByte(s, '/')
	return RepoRef{Owner: s[:i], Name: s[i+1:]}, nil
}

// FullName returns "owner/name".
func (r RepoRef) FullName() string { return r.Owner + "/" + r.Name }
