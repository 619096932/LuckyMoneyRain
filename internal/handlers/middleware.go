package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"hongbao/internal/auth"
)

func (s *Server) AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := getBearerToken(c.Request)
		if token == "" {
			// 兼容 ?token=
			token = c.Query("token")
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ParseToken(s.JWTSecret, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if err := s.validateSession(claims.UserID, claims.SessionID); err != nil {
			status := http.StatusUnauthorized
			if err != errInvalidSession {
				status = http.StatusServiceUnavailable
			}
			c.AbortWithStatusJSON(status, gin.H{"error": "session invalid"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("phone", claims.Phone)
		c.Set("admin", claims.IsAdmin)
		c.Set("sid", claims.SessionID)
		c.Next()
	}
}

func (s *Server) AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 支持管理员Token
		adminToken := c.GetHeader("X-Admin-Token")
		if adminToken != "" && adminToken == s.Cfg.AdminToken {
			c.Next()
			return
		}

		// 兼容 Bearer
		token := getBearerToken(c.Request)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing admin token"})
			return
		}
		claims, err := auth.ParseToken(s.JWTSecret, token)
		if err != nil || !claims.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin required"})
			return
		}
		if err := s.validateSession(claims.UserID, claims.SessionID); err != nil {
			status := http.StatusUnauthorized
			if err != errInvalidSession {
				status = http.StatusServiceUnavailable
			}
			c.AbortWithStatusJSON(status, gin.H{"error": "session invalid"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("phone", claims.Phone)
		c.Set("admin", true)
		c.Next()
	}
}
