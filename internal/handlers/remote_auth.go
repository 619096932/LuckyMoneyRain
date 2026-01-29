package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type remoteRegisterRequest struct {
	Key       string `json:"key"`
	Phone     string `json:"phone"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
}

func (s *Server) RemoteRegister(c *gin.Context) {
	if strings.TrimSpace(s.Cfg.RemoteAPIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "remote api not configured"})
		return
	}
	var req remoteRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	key := strings.TrimSpace(c.GetHeader("X-Remote-Key"))
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
	if key == "" || key != s.Cfg.RemoteAPIKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid key"})
		return
	}
	user, err := s.upsertUserProfile(req.Phone, req.Nickname, req.AvatarURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	token, err := s.SignToken(user.ID, user.Phone, user.IsAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": user})
}
