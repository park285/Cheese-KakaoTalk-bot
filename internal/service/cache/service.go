package cache

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"sync"
	"time"

	"github.com/kapu/kakao-cheese-bot-go/pkg/errors"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type CacheService struct {
	client    *redis.Client
	logger    *zap.Logger
	closeOnce sync.Once
}

const memberHashKey = "hololive:members"

type CacheConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

func NewCacheService(cfg CacheConfig, logger *zap.Logger) (*CacheService, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		MaxRetries:   3,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, errors.NewCacheError("failed to connect to Redis", "ping", "", err)
	}

	logger.Info("Redis connected",
		zap.String("addr", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)),
		zap.Int("db", cfg.DB),
	)

	return &CacheService{
		client: client,
		logger: logger,
	}, nil
}

func (c *CacheService) Get(ctx context.Context, key string, dest any) error {
	value, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		c.logger.Error("Cache get failed", zap.String("key", key), zap.Error(err))
		return errors.NewCacheError("get failed", "get", key, err)
	}

	if dest != nil {
		if err := json.Unmarshal([]byte(value), dest); err != nil {
			c.logger.Error("Cache unmarshal failed", zap.String("key", key), zap.Error(err))
			return errors.NewCacheError("unmarshal failed", "get", key, err)
		}
	}

	return nil
}

func (c *CacheService) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	jsonData, err := json.Marshal(value)
	if err != nil {
		return errors.NewCacheError("marshal failed", "set", key, err)
	}

	if ttl > 0 {
		err = c.client.Set(ctx, key, jsonData, ttl).Err()
	} else {
		err = c.client.Set(ctx, key, jsonData, 0).Err()
	}

	if err != nil {
		c.logger.Error("Cache set failed", zap.String("key", key), zap.Error(err))
		return errors.NewCacheError("set failed", "set", key, err)
	}

	return nil
}

func (c *CacheService) Del(ctx context.Context, key string) error {
	if err := c.client.Del(ctx, key).Err(); err != nil {
		c.logger.Error("Cache delete failed", zap.String("key", key), zap.Error(err))
		return errors.NewCacheError("delete failed", "del", key, err)
	}
	return nil
}

func (c *CacheService) DelMany(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}

	deleted, err := c.client.Del(ctx, keys...).Result()
	if err != nil {
		c.logger.Error("Cache delete many failed", zap.Int("count", len(keys)), zap.Error(err))
		return 0, errors.NewCacheError("delete many failed", "del", fmt.Sprintf("%d keys", len(keys)), err)
	}

	return deleted, nil
}

func (c *CacheService) Keys(ctx context.Context, pattern string) ([]string, error) {
	keys, err := c.client.Keys(ctx, pattern).Result()
	if err != nil {
		c.logger.Error("Cache keys search failed", zap.String("pattern", pattern), zap.Error(err))
		return []string{}, errors.NewCacheError("keys search failed", "keys", pattern, err)
	}
	return keys, nil
}

func (c *CacheService) SAdd(ctx context.Context, key string, members []string) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}

	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}

	added, err := c.client.SAdd(ctx, key, args...).Result()
	if err != nil {
		c.logger.Error("Cache sadd failed", zap.String("key", key), zap.Error(err))
		return 0, errors.NewCacheError("sadd failed", "sadd", key, err)
	}

	return added, nil
}

func (c *CacheService) SRem(ctx context.Context, key string, members []string) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}

	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}

	removed, err := c.client.SRem(ctx, key, args...).Result()
	if err != nil {
		c.logger.Error("Cache srem failed", zap.String("key", key), zap.Error(err))
		return 0, errors.NewCacheError("srem failed", "srem", key, err)
	}

	return removed, nil
}

func (c *CacheService) SMembers(ctx context.Context, key string) ([]string, error) {
	members, err := c.client.SMembers(ctx, key).Result()
	if err != nil {
		c.logger.Error("Cache smembers failed", zap.String("key", key), zap.Error(err))
		return []string{}, errors.NewCacheError("smembers failed", "smembers", key, err)
	}
	return members, nil
}

func (c *CacheService) SIsMember(ctx context.Context, key, member string) (bool, error) {
	exists, err := c.client.SIsMember(ctx, key, member).Result()
	if err != nil {
		c.logger.Error("Cache sismember failed", zap.String("key", key), zap.Error(err))
		return false, errors.NewCacheError("sismember failed", "sismember", key, err)
	}
	return exists, nil
}

func (c *CacheService) HSet(ctx context.Context, key, field, value string) error {
	if err := c.client.HSet(ctx, key, field, value).Err(); err != nil {
		c.logger.Error("Cache hset failed", zap.String("key", key), zap.String("field", field), zap.Error(err))
		return errors.NewCacheError("hset failed", "hset", key, err)
	}
	return nil
}

func (c *CacheService) HMSet(ctx context.Context, key string, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	if err := c.client.HMSet(ctx, key, fields).Err(); err != nil {
		c.logger.Error("Cache hmset failed", zap.String("key", key), zap.Int("fields", len(fields)), zap.Error(err))
		return errors.NewCacheError("hmset failed", "hmset", key, err)
	}
	return nil
}

func (c *CacheService) HGet(ctx context.Context, key, field string) (string, error) {
	value, err := c.client.HGet(ctx, key, field).Result()
	if err == redis.Nil {
		return "", nil // Field doesn't exist - not an error
	}
	if err != nil {
		c.logger.Error("Cache hget failed", zap.String("key", key), zap.String("field", field), zap.Error(err))
		return "", errors.NewCacheError("hget failed", "hget", key, err)
	}
	return value, nil
}

func (c *CacheService) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	values, err := c.client.HGetAll(ctx, key).Result()
	if err != nil {
		c.logger.Error("Cache hgetall failed", zap.String("key", key), zap.Error(err))
		return map[string]string{}, errors.NewCacheError("hgetall failed", "hgetall", key, err)
	}
	return values, nil
}

func (c *CacheService) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := c.client.Expire(ctx, key, ttl).Err(); err != nil {
		c.logger.Error("Cache expire failed", zap.String("key", key), zap.Error(err))
		return errors.NewCacheError("expire failed", "expire", key, err)
	}
	return nil
}

func (c *CacheService) Exists(ctx context.Context, key string) (bool, error) {
	count, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		c.logger.Error("Cache exists failed", zap.String("key", key), zap.Error(err))
		return false, errors.NewCacheError("exists failed", "exists", key, err)
	}
	return count > 0, nil
}

func (c *CacheService) Close() error {
	var closeErr error

	c.closeOnce.Do(func() {
		if c.client == nil {
			return
		}

		if err := c.client.Close(); err != nil {
			if stdErrors.Is(err, redis.ErrClosed) {
				c.logger.Info("Redis connection already closed")
				return
			}

			c.logger.Error("Failed to close Redis connection", zap.Error(err))
			closeErr = err
			return
		}

		c.logger.Info("Redis disconnected")
	})

	return closeErr
}

func (c *CacheService) IsConnected(ctx context.Context) bool {
	return c.client.Ping(ctx).Err() == nil
}

func (c *CacheService) WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Redis to be ready")
		case <-ticker.C:
			if c.IsConnected(ctx) {
				return nil
			}
		}
	}
}

func (c *CacheService) InitializeMemberDatabase(ctx context.Context, memberData map[string]string) error {
	if err := c.client.Del(ctx, memberHashKey).Err(); err != nil {
		c.logger.Error("Failed to clear member database", zap.Error(err))
		return errors.NewCacheError("del failed", "del", memberHashKey, err)
	}

	if len(memberData) == 0 {
		c.logger.Info("Member database cleared (no members provided)")
		return nil
	}

	values := make([]any, 0, len(memberData)*2)
	for name, channelID := range memberData {
		values = append(values, name, channelID)
	}

	if err := c.client.HSet(ctx, memberHashKey, values...).Err(); err != nil {
		c.logger.Error("Failed to initialize member database", zap.Error(err))
		return errors.NewCacheError("hset failed", "hset", memberHashKey, err)
	}

	c.logger.Info("Member database initialized",
		zap.Int("members", len(memberData)),
	)
	return nil
}

func (c *CacheService) GetMemberChannelID(ctx context.Context, memberName string) (string, error) {
	if memberName == "" {
		return "", nil
	}

	value, err := c.client.HGet(ctx, memberHashKey, memberName).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		c.logger.Error("Failed to get member channel ID", zap.String("member", memberName), zap.Error(err))
		return "", errors.NewCacheError("hget failed", "hget", memberHashKey, err)
	}
	return value, nil
}

func (c *CacheService) GetAllMembers(ctx context.Context) (map[string]string, error) {
	values, err := c.client.HGetAll(ctx, memberHashKey).Result()
	if err != nil {
		c.logger.Error("Failed to get all members", zap.Error(err))
		return map[string]string{}, errors.NewCacheError("hgetall failed", "hgetall", memberHashKey, err)
	}
	return values, nil
}

func (c *CacheService) AddMember(ctx context.Context, memberName, channelID string) error {
	if memberName == "" || channelID == "" {
		return fmt.Errorf("member name and channel ID must be provided")
	}

	if err := c.client.HSet(ctx, memberHashKey, memberName, channelID).Err(); err != nil {
		c.logger.Error("Failed to add member", zap.String("member", memberName), zap.String("channel_id", channelID), zap.Error(err))
		return errors.NewCacheError("hset failed", "hset", memberHashKey, err)
	}
	c.logger.Info("Member added/updated",
		zap.String("member", memberName),
		zap.String("channel_id", channelID),
	)
	return nil
}

func (c *CacheService) GetRedisClient() *redis.Client {
	return c.client
}
