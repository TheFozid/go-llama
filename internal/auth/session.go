package auth

import (
	"context"
	"fmt"
	"time"
	"strings"

	"github.com/redis/go-redis/v9"
)

const sessionKeyFmt = "session:%d"

func SetSession(rdb *redis.Client, userId uint, token string, duration time.Duration) error {
	ctx := context.Background()
	key := fmt.Sprintf(sessionKeyFmt, userId)
	return rdb.Set(ctx, key, token, duration).Err()
}

func GetSession(rdb *redis.Client, userId uint) (string, error) {
	ctx := context.Background()
	key := fmt.Sprintf(sessionKeyFmt, userId)
	return rdb.Get(ctx, key).Result()
}

func DeleteSession(rdb *redis.Client, userId uint) error {
	ctx := context.Background()
	key := fmt.Sprintf(sessionKeyFmt, userId)
	return rdb.Del(ctx, key).Err()
}

// OnlineUserCount returns the number of unique users with active sessions.
func OnlineUserCount(rdb *redis.Client) (int, error) {
	ctx := context.Background()
	var cursor uint64
	userIds := make(map[string]struct{})
	for {
		keys, newCursor, err := rdb.Scan(ctx, cursor, "session:*", 100).Result()
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) == 2 && parts[0] == "session" && parts[1] != "" {
				userIds[parts[1]] = struct{}{}
			}
		}
		if newCursor == 0 {
			break
		}
		cursor = newCursor
	}
	return len(userIds), nil
}
