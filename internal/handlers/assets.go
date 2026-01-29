package handlers

import "github.com/gin-gonic/gin"

// GetAssets returns public asset URLs for frontend usage.
func (s *Server) GetAssets(c *gin.Context) {
	c.JSON(200, gin.H{
		"intro_bgm_url": s.Cfg.IntroBGMURL,
		"game_bgm_url":  s.Cfg.GameBGMURL,
	})
}
