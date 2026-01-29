package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) InitReset(c *gin.Context) {
	secret := s.Cfg.InitSecret
	if secret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "init secret not configured"})
		return
	}
	provided := c.GetHeader("X-Init-Secret")
	if provided == "" {
		provided = c.Query("secret")
	}
	if provided == "" {
		provided = c.PostForm("secret")
	}
	if provided != secret {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid init secret"})
		return
	}

	if s.Game != nil {
		s.Game.SetCurrent(nil)
	}

	if s.Redis != nil {
		ctx := context.Background()
		patterns := []string{"round:*", "online:*", "session:uid:*", "sms:code:*"}
		for _, pattern := range patterns {
			var cursor uint64
			for {
				keys, next, err := s.Redis.Scan(ctx, cursor, pattern, 1000).Result()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "redis scan error"})
					return
				}
				if len(keys) > 0 {
					_ = s.Redis.Del(ctx, keys...).Err()
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
	}

	if s.DB != nil {
		// 使用 TRUNCATE 真正清空表并重置自增ID
		// 注意：需要按外键依赖顺序删除，先子表后父表
		// TRUNCATE 不支持事务，需要先关闭外键检查
		if _, err := s.DB.Exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error: disable fk checks"})
			return
		}
		cleanupTables := []string{
			"award_details",
			"award_batches",
			"click_events",
			"scores",
			"wallet_ledger",
			"wallets",
			"withdraw_requests",
			"user_alipay_accounts",
			"round_whitelist",
			"rounds",
			"users",
		}
		for _, table := range cleanupTables {
			if _, err := s.DB.Exec("TRUNCATE TABLE " + table); err != nil {
				// 尝试恢复外键检查
				_, _ = s.DB.Exec("SET FOREIGN_KEY_CHECKS = 1")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "db error: truncate " + table})
				return
			}
		}
		if _, err := s.DB.Exec("SET FOREIGN_KEY_CHECKS = 1"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error: enable fk checks"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
