package common

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"icode.baidu.com/baidu/gdp/redis"
)

const sandboxGrantKeyTTL = time.Hour
const sandboxApplyCardKeyTTL = 7 * 24 * time.Hour

func sandboxGrantKey(sandboxID, username string) string {
	return fmt.Sprintf("sandbox:grant:%s:%s", sandboxID, username)
}

func sandboxApplyCardKey(sandboxID, applyUser string) string {
	return fmt.Sprintf("sandbox:apply:card:%s:%s", sandboxID, applyUser)
}

// ApplyCardRecord 发卡后记录，用于后续更新卡片
type ApplyCardRecord struct {
	Receiver    string `json:"receiver"`
	ModifyToken string `json:"modifyToken"`
	CardID      string `json:"cardId"`
}

// SetApplyCardRecord 发卡成功后保存记录到 Redis
func SetApplyCardRecord(ctx context.Context, redisClient redis.Client, sandboxID, applyUser string, record *ApplyCardRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return redisClient.Set(ctx, sandboxApplyCardKey(sandboxID, applyUser), string(data), sandboxApplyCardKeyTTL).Err()
}

// GetApplyCardRecord 获取发卡记录
func GetApplyCardRecord(ctx context.Context, redisClient redis.Client, sandboxID, applyUser string) (*ApplyCardRecord, error) {
	val, err := redisClient.Get(ctx, sandboxApplyCardKey(sandboxID, applyUser)).Result()
	if err != nil {
		return nil, err
	}
	var record ApplyCardRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// SetSandboxGrantCache 审批通过后写授权缓存
func SetSandboxGrantCache(ctx context.Context, redisClient redis.Client, sandboxID, username string) error {
	return redisClient.Set(ctx, sandboxGrantKey(sandboxID, username), "1", sandboxGrantKeyTTL).Err()
}

// HasSandboxGrant 检查是否存在授权缓存
func HasSandboxGrant(ctx context.Context, redisClient redis.Client, sandboxID, username string) bool {
	n, err := redisClient.Exists(ctx, sandboxGrantKey(sandboxID, username)).Result()
	return err == nil && n > 0
}
