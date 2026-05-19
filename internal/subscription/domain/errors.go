package domain

import "errors"

var (
	ErrRepoNotFound      = errors.New("repository not found")
	ErrTokenNotFound     = errors.New("token not found")
	ErrTokenExpired      = errors.New("token expired")
	ErrAlreadyExists     = errors.New("subscription already exists")
	ErrInvalidRepoFormat = errors.New("invalid repository format, expected owner/name")
)
