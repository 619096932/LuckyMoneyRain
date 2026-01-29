package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"hongbao/internal/game"
	"hongbao/internal/models"
)

type clickRequest struct {
	RoundID  int64  `json:"round_id"`
	DropID   int    `json:"drop_id"`
	ClientTS int64  `json:"client_ts"`
	Sign     string `json:"sign"`
}

func (s *Server) GetCurrentRound(c *gin.Context) {
	rt := s.Game.GetCurrent()
	if rt == nil {
		c.JSON(http.StatusOK, gin.H{"round": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"round": rt.Round})
}

func (s *Server) GetGameState(c *gin.Context) {
	rt := s.Game.GetCurrent()
	if rt == nil {
		c.JSON(http.StatusOK, gin.H{"round": nil})
		return
	}
	withSlices := true
	if v := strings.ToLower(c.Query("with_slices")); v != "" {
		if v == "0" || v == "false" || v == "no" {
			withSlices = false
		}
	}
	uid := c.GetInt64("uid")
	s.MarkOnline(uid)
	score, _ := s.Redis.ZScore(context.Background(), scoreZSetKey(rt.Round.ID), scoreMember(uid)).Result()
	eligible := s.isWhitelisted(rt.Round.ID, uid)
	whitelistCount, _ := s.Redis.SCard(context.Background(), whitelistKey(rt.Round.ID)).Result()
	onlineCount := len(s.getActiveOnlineUserIDs(context.Background()))
	payloadRound := rt.Round
	if !eligible && payloadRound.Status != models.RoundWaiting && payloadRound.Status != models.RoundLocked {
		payloadRound.Status = models.RoundLocked
	}
	payload := gin.H{
		"round":           payloadRound,
		"score":           int(score),
		"eligible":        eligible,
		"online_count":    onlineCount,
		"whitelist_count": int(whitelistCount),
		"server_time":     time.Now().UnixMilli(),
	}
	if withSlices && eligible && (payloadRound.Status == models.RoundRunning || payloadRound.Status == models.RoundCountdown || payloadRound.Status == models.RoundLocked) {
		manifests := make([]game.SliceManifest, 0, len(rt.Slices))
		for _, s := range rt.Slices {
			manifest := s.Manifest
			manifest.Seed = game.UserSeed(manifest.Seed, uid)
			manifests = append(manifests, manifest)
		}
		payload["slices"] = manifests
	}
	c.JSON(http.StatusOK, payload)
}

func (s *Server) Click(c *gin.Context) {
	var req clickRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RoundID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	uid := c.GetInt64("uid")
	s.MarkOnline(uid)
	if !s.verifySign(uid, req.RoundID, req.DropID, req.ClientTS, req.Sign) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid sign"})
		return
	}

	// 白名单校验
	ctx := context.Background()
	if !s.isWhitelisted(req.RoundID, uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "not whitelisted"})
		return
	}

	now := time.Now().UnixMilli()
	effectiveNow := now
	if req.ClientTS > 0 {
		maxSkew := int64(s.Cfg.TimeSkewMS + s.Cfg.ClickGraceMS)
		if maxSkew < 3000 {
			maxSkew = 3000
		}
		diff := req.ClientTS - now
		if diff < 0 {
			diff = -diff
		}
		if diff <= maxSkew {
			effectiveNow = req.ClientTS
		}
	}
	delta, total, isBomb, err := s.Game.ValidateClick(ctx, uid, req.RoundID, req.DropID, effectiveNow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 写入点击流
	_ = s.Redis.XAdd(ctx, &redis.XAddArgs{
		Stream: clickStreamKey(req.RoundID),
		Values: map[string]interface{}{
			"uid":     uid,
			"drop_id": req.DropID,
			"delta":   delta,
			"bomb":    boolToInt(isBomb),
			"ts":      now,
		},
	}).Err()
	_ = s.Redis.Expire(ctx, clickStreamKey(req.RoundID), s.roundKeyTTL(req.RoundID)).Err()
	_ = s.bumpQPS(ctx, req.RoundID, now)

	c.JSON(http.StatusOK, gin.H{"delta": delta, "total": total, "bomb": isBomb})
}

func (s *Server) GetResult(c *gin.Context) {
	roundIDStr := c.Query("round_id")
	roundID, _ := strconv.ParseInt(roundIDStr, 10, 64)
	uid := c.GetInt64("uid")
	row := s.DB.QueryRow(`SELECT ad.score, ad.amount, ad.base_amount, ad.lucky_amount FROM award_details ad JOIN award_batches ab ON ad.batch_id=ab.id WHERE ab.round_id=? AND ad.user_id=? ORDER BY ad.created_at DESC LIMIT 1`, roundID, uid)
	var score int
	var amount, baseAmount, luckyAmount int64
	if err := row.Scan(&score, &amount, &baseAmount, &luckyAmount); err != nil {
		c.JSON(http.StatusOK, gin.H{"score": 0, "amount": 0})
		return
	}
	c.JSON(http.StatusOK, gin.H{"score": score, "amount": amount, "base_amount": baseAmount, "lucky_amount": luckyAmount})
}

func (s *Server) verifySign(uid int64, roundID int64, dropID int, clientTS int64, sign string) bool {
	if sign == "" || s.Cfg.GameSignSecret == "" {
		return true
	}
	msg := fmt.Sprintf("%d|%d|%d|%d", uid, roundID, dropID, clientTS)
	h := hmac.New(sha256.New, []byte(s.Cfg.GameSignSecret))
	_, _ = h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil)) == sign
}

func clickStreamKey(roundID int64) string {
	return "round:" + strconv.FormatInt(roundID, 10) + ":clicks"
}

func (s *Server) bumpQPS(ctx context.Context, roundID int64, nowMS int64) error {
	sec := nowMS / 1000
	key := fmt.Sprintf("round:%d:qps:%d", roundID, sec)
	if err := s.Redis.Incr(ctx, key).Err(); err != nil {
		return err
	}
	return s.Redis.Expire(ctx, key, 10*time.Second).Err()
}
