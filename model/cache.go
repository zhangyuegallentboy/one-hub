package model

import (
	"context"
	"fmt"
	"one-api/common/cache"
	"one-api/common/config"
	"one-api/common/logger"
	"one-api/common/redis"
	"strconv"
	"time"
)

var (
	TokenCacheSeconds           = 0
	UserGroupCacheKey           = "user_group:%d"
	UserTokensKey               = "token:%s"
	UsernameCacheKey            = "user_name:%d"
	UserQuotaCacheKey           = "user_quota:%d"
	UserEnabledCacheKey         = "user_enabled:%d"
	UserRealtimeQuotaKey        = "user_realtime_quota:%d"
	UserRealtimeQuotaExpiration = 24 * time.Hour

	OldUserTokensCacheKey = "old_user_tokens_cache"
)

func CacheGetTokenByKey(key string) (*Token, error) {
	token, err := cache.GetOrSetCache(
		fmt.Sprintf(UserTokensKey, key),
		time.Duration(TokenCacheSeconds)*time.Second,
		func() (*Token, error) {
			return GetTokenByKey(key)
		},
		cache.CacheTimeout)

	return token, err
}

func CacheGetUserGroup(id int) (group string, err error) {
	if !config.RedisEnabled {
		return GetUserGroup(id)
	}

	group, err = cache.GetOrSetCache(
		fmt.Sprintf(UserGroupCacheKey, id),
		time.Duration(TokenCacheSeconds)*time.Second,
		func() (string, error) {
			groupId, err := GetUserGroup(id)
			if err != nil {
				return "", err
			}
			return groupId, nil
		},
		cache.CacheTimeout)

	return group, err
}

func CacheGetUserQuota(id int) (quota int, err error) {
	if !config.RedisEnabled {
		return GetUserQuota(id)
	}
	quotaString, err := redis.RedisGet(fmt.Sprintf(UserQuotaCacheKey, id))
	if err != nil {
		quota, err = GetUserQuota(id)
		if err != nil {
			return 0, err
		}
		err = redis.RedisSet(fmt.Sprintf(UserQuotaCacheKey, id), fmt.Sprintf("%d", quota), time.Duration(TokenCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set user quota error: " + err.Error())
		}
		return quota, err
	}
	quota, err = strconv.Atoi(quotaString)
	return quota, err
}

func CacheUpdateUserQuota(id int) error {
	if !config.RedisEnabled {
		return nil
	}
	quota, err := GetUserQuota(id)
	if err != nil {
		return err
	}
	err = redis.RedisSet(fmt.Sprintf(UserQuotaCacheKey, id), fmt.Sprintf("%d", quota), time.Duration(TokenCacheSeconds)*time.Second)
	return err
}

func CacheDecreaseUserQuota(id int, quota int) error {
	if !config.RedisEnabled {
		return nil
	}
	err := redis.RedisDecrease(fmt.Sprintf(UserQuotaCacheKey, id), int64(quota))
	return err
}

func CacheIsUserEnabled(userId int) (bool, error) {
	enabled, err := cache.GetOrSetCache(
		fmt.Sprintf(UserEnabledCacheKey, userId),
		time.Duration(TokenCacheSeconds)*time.Second,
		func() (bool, error) {
			enabled, err := IsUserEnabled(userId)
			if err != nil {
				return false, err
			}
			return enabled, nil
		},
		cache.CacheTimeout)

	return enabled, err
}

func CacheGetUsername(id int) (username string, err error) {
	if !config.RedisEnabled {
		return GetUsernameById(id), nil
	}

	username, err = cache.GetOrSetCache(
		fmt.Sprintf(UsernameCacheKey, id),
		time.Duration(TokenCacheSeconds)*time.Second,
		func() (string, error) {
			username := GetUsernameById(id)
			if username == "" {
				return "", fmt.Errorf("user %d not found", id)
			}

			return username, nil
		},
		cache.CacheTimeout)

	return username, err
}

func CacheDecreaseUserRealtimeQuota(id int, quota int) (int64, error) {
	if !config.RedisEnabled {
		return 0, nil
	}
	return CacheUpdateUserRealtimeQuota(id, -quota)
}

func CacheIncreaseUserRealtimeQuota(id int, quota int) (int64, error) {
	if !config.RedisEnabled {
		return 0, nil
	}
	return CacheUpdateUserRealtimeQuota(id, quota)
}

var (
	updateQuotaScript = redis.NewScript(`
		local key = KEYS[1]
		local increment = tonumber(ARGV[1])
		local expiration = tonumber(ARGV[2])

		local exists = redis.call("EXISTS", key)
		if exists == 0 then
			if increment < 0 then
				return 0
			end
			redis.call("SET", key, "0", "EX", expiration)
		end

		local newValue = redis.call("INCRBY", key, increment)
		redis.call("EXPIRE", key, expiration)

		return newValue
	`)
)

func CacheUpdateUserRealtimeQuota(id int, quota int) (int64, error) {
	if !config.RedisEnabled {
		return 0, nil
	}
	key := fmt.Sprintf(UserRealtimeQuotaKey, id)

	newValue, err := updateQuotaScript.Run(context.Background(), redis.GetRedisClient(), []string{key}, quota, int(UserRealtimeQuotaExpiration.Seconds())).Int64()
	if err != nil {
		return 0, fmt.Errorf("更新用户配额失败: %w", err)
	}

	return newValue, nil
}

func HandleOldTokenMaxId() {
	if config.OldTokenMaxId == 0 || !config.RedisEnabled {
		return
	}

	// 检测OldUserTokensCacheKey是否存在
	exists, _ := redis.RedisExists(OldUserTokensCacheKey)
	if exists {
		return
	}
	const batchSize = 1000
	var offset int

	for {
		var tokenKeys []interface{}
		result := DB.Model(&Token{}).
			Where("id <= ?", config.OldTokenMaxId).
			Limit(batchSize).
			Offset(offset).
			Pluck("key", &tokenKeys)

		if result.Error != nil {
			logger.SysError("查询旧token失败: " + result.Error.Error())
			return
		}

		if len(tokenKeys) == 0 {
			if offset == 0 {
				logger.SysLog("没有找到旧token")
			}
			break
		}

		if err := redis.RedisSAdd(OldUserTokensCacheKey, tokenKeys...); err != nil {
			logger.SysError("添加旧token到Redis失败: " + err.Error())
		}

		logger.SysLog(fmt.Sprintf("已处理 %d 个旧token", offset+len(tokenKeys)))
		offset += batchSize

		time.Sleep(100 * time.Millisecond)
	}
}
