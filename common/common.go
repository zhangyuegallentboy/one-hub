package common

import (
	"errors"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"gorm.io/gorm"
	"log"
	"math"
	"one-api/common/config"
	"time"
)

func LogQuota(quota int) string {
	if config.DisplayInCurrencyEnabled {
		if quota < 0 {
			return fmt.Sprintf("-＄%.6f 额度", math.Abs(float64(quota)/config.QuotaPerUnit))
		}
		return fmt.Sprintf("＄%.6f 额度", float64(quota)/config.QuotaPerUnit)
	} else {
		return fmt.Sprintf("%d 点额度", quota)
	}
}

func IsRetryableError(err error) bool {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	return true
}

func Retry(operation func() error) error {
	bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(time.Millisecond*1), 3)
	return backoff.Retry(func() error {
		err := operation()
		if err != nil {
			if !IsRetryableError(err) {
				return &backoff.PermanentError{Err: err}
			}
			log.Printf("GORM operation failed, retrying... Error: %v", err)
			return err // 返回错误，backoff 库会根据策略决定是否继续重试
		}
		return nil // 操作成功，返回 nil 停止重试
	}, bo)
}
