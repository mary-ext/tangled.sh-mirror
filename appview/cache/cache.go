package cache

import "github.com/redis/go-redis/v9"

type Cache struct {
	*redis.Client
}

func New(addr string) *Cache {
	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
	})
	return &Cache{rdb}
}
