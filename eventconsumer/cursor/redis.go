package cursor

import (
	"context"
	"fmt"
	"strconv"

	"tangled.sh/tangled.sh/core/appview/cache"
)

const (
	cursorKey = "cursor:%s"
)

type RedisStore struct {
	rdb *cache.Cache
}

func NewRedisCursorStore(cache *cache.Cache) RedisStore {
	return RedisStore{
		rdb: cache,
	}
}

func (r *RedisStore) Set(knot string, cursor int64) {
	key := fmt.Sprintf(cursorKey, knot)
	r.rdb.Set(context.Background(), key, cursor, 0)
}

func (r *RedisStore) Get(knot string) (cursor int64) {
	key := fmt.Sprintf(cursorKey, knot)
	val, err := r.rdb.Get(context.Background(), key).Result()
	if err != nil {
		return 0
	}
	cursor, err = strconv.ParseInt(val, 10, 64)
	if err != nil {
		// TODO: log here
		return 0
	}

	return cursor
}
