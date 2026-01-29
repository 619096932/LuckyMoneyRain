package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const withdrawSwitchKey = "cfg:withdraw_enabled"

func (s *Server) loadWithdrawSwitch() {
	if s.Redis == nil {
		return
	}
	val, err := s.Redis.Get(context.Background(), withdrawSwitchKey).Result()
	if err != nil {
		return
	}
	s.withdrawEnabled.Store(parseBool(val, s.withdrawEnabled.Load()))
}

func (s *Server) IsWithdrawEnabled() bool {
	return s.withdrawEnabled.Load()
}

func (s *Server) SetWithdrawEnabled(enabled bool) {
	s.withdrawEnabled.Store(enabled)
	if s.Redis == nil {
		return
	}
	val := "0"
	if enabled {
		val = "1"
	}
	_ = s.Redis.Set(context.Background(), withdrawSwitchKey, val, 0).Err()
}

func (s *Server) GetWithdrawSwitch(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"enabled": s.IsWithdrawEnabled()})
}

func (s *Server) SetWithdrawSwitch(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	s.SetWithdrawEnabled(*req.Enabled)
	c.JSON(http.StatusOK, gin.H{"enabled": s.IsWithdrawEnabled()})
}

func parseBool(val string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}
