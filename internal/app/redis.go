package app

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

type Config struct {
	URL string
}

func NewRedisClient(url string) (*goredis.Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	rdb := goredis.NewClient(opts)
	if err = rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return rdb, nil
}
