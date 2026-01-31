package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
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

type slicePayload struct {
	SliceID       int     `json:"slice_id"`
	StartAtMS     int64   `json:"start_at"`
	DurationMS    int     `json:"duration_ms"`
	DropCount     int     `json:"drop_count"`
	BombCount     int     `json:"bomb_count"`
	BigCount      int     `json:"big_count"`
	EmptyCount    int     `json:"empty_count"`
	BigMultiplier float64 `json:"big_multiplier"`
	WindowMS      int     `json:"window_ms"`
	ScoreTotal    int     `json:"score_total"`
	OffsetsMS     []int   `json:"offsets_ms"`
	DropTypes     []int   `json:"drop_types"`
	SeedCommit    string  `json:"seed_commit"`
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
	if key, ok := s.gameSignKey(c.GetString("sid")); ok {
		payload["sign_key"] = hex.EncodeToString(key)
	}
	if withSlices && eligible && (payloadRound.Status == models.RoundRunning || payloadRound.Status == models.RoundCountdown || payloadRound.Status == models.RoundLocked) {
		manifests := make([]slicePayload, 0, len(rt.Slices))
		for _, s := range rt.Slices {
			manifests = append(manifests, buildSlicePayload(s.Manifest, rt.RevealSalt, uid))
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
	sid := c.GetString("sid")
	if !s.verifySign(uid, sid, req.RoundID, req.DropID, req.ClientTS, req.Sign) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid sign"})
		return
	}

	delta, total, isBomb, err := s.processClick(context.Background(), uid, req.RoundID, req.DropID, req.ClientTS)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"delta": delta, "total": total, "bomb": isBomb})
}

func (s *Server) GetResult(c *gin.Context) {
	roundIDStr := c.Query("round_id")
	roundID, _ := strconv.ParseInt(roundIDStr, 10, 64)
	uid := c.GetInt64("uid")
	row := s.DB.QueryRow(`SELECT ad.score, ad.amount, ad.base_amount, ad.lucky_amount FROM award_details ad JOIN award_batches ab ON ad.batch_id=ab.id WHERE ab.round_id=? AND ab.status <> 'VOID' AND ad.user_id=? ORDER BY ad.created_at DESC LIMIT 1`, roundID, uid)
	var score int
	var amount, baseAmount, luckyAmount int64
	if err := row.Scan(&score, &amount, &baseAmount, &luckyAmount); err != nil {
		c.JSON(http.StatusOK, gin.H{"score": 0, "amount": 0})
		return
	}
	c.JSON(http.StatusOK, gin.H{"score": score, "amount": amount, "base_amount": baseAmount, "lucky_amount": luckyAmount})
}

func (s *Server) GetGameReveal(c *gin.Context) {
	rt := s.Game.GetCurrent()
	if rt == nil {
		c.JSON(http.StatusOK, gin.H{"round": nil})
		return
	}
	roundIDStr := c.Query("round_id")
	roundID, _ := strconv.ParseInt(roundIDStr, 10, 64)
	if roundID == 0 {
		roundID = rt.Round.ID
	}
	if rt.Round.ID != roundID {
		c.JSON(http.StatusNotFound, gin.H{"error": "round not found"})
		return
	}
	switch rt.Round.Status {
	case models.RoundReadyDraw, models.RoundDrawing, models.RoundPendingConfirm, models.RoundFinished:
	default:
		c.JSON(http.StatusForbidden, gin.H{"error": "reveal not available"})
		return
	}
	if rt.RevealSalt == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reveal not available"})
		return
	}
	uid := c.GetInt64("uid")
	slices := make([]gin.H, 0, len(rt.Slices))
	for _, s := range rt.Slices {
		userSeed := game.UserSeed(s.Manifest.Seed, uid)
		slices = append(slices, gin.H{
			"slice_id":    s.Manifest.SliceID,
			"seed":        userSeed,
			"seed_commit": seedCommit(userSeed, rt.RevealSalt),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"round_id": rt.Round.ID,
		"salt":     rt.RevealSalt,
		"slices":   slices,
	})
}

func (s *Server) verifySign(uid int64, sessionID string, roundID int64, dropID int, clientTS int64, sign string) bool {
	sign = strings.TrimSpace(sign)
	if sign == "" {
		return false
	}
	key, ok := s.gameSignKey(sessionID)
	if !ok {
		return false
	}
	msg := fmt.Sprintf("%d|%d|%d|%d", uid, roundID, dropID, clientTS)
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(msg))
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sign))
}

func clickStreamKey(roundID int64) string {
	return "round:" + strconv.FormatInt(roundID, 10) + ":clicks"
}

func (s *Server) processClick(ctx context.Context, uid int64, roundID int64, dropID int, clientTS int64) (int, int, bool, error) {
	// 白名单校验
	if !s.isWhitelisted(roundID, uid) {
		return 0, 0, false, errors.New("not whitelisted")
	}

	now := time.Now().UnixMilli()
	effectiveNow := now
	if clientTS > 0 {
		maxSkew := int64(s.Cfg.TimeSkewMS + s.Cfg.ClickGraceMS)
		if maxSkew < 3000 {
			maxSkew = 3000
		}
		diff := clientTS - now
		if diff < 0 {
			diff = -diff
		}
		if diff <= maxSkew {
			effectiveNow = clientTS
		}
	}
	delta, total, isBomb, err := s.Game.ValidateClick(ctx, uid, roundID, dropID, effectiveNow)
	if err != nil {
		return 0, 0, false, err
	}

	// 写入点击流（可选）
	if s.Cfg.ClickStreamEnabled {
		pipe := s.Redis.Pipeline()
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: clickStreamKey(roundID),
			Values: map[string]interface{}{
				"uid":     uid,
				"drop_id": dropID,
				"delta":   delta,
				"bomb":    boolToInt(isBomb),
				"ts":      now,
			},
		})
		pipe.Expire(ctx, clickStreamKey(roundID), s.roundKeyTTL(roundID))
		_, _ = pipe.Exec(ctx)
	}
	_ = s.bumpQPS(ctx, roundID, now)

	return delta, total, isBomb, nil
}

func (s *Server) gameSignKey(sessionID string) ([]byte, bool) {
	secret := strings.TrimSpace(s.Cfg.GameSignSecret)
	if secret == "" || secret == "change-me" {
		return nil, false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, false
	}
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(sessionID))
	return h.Sum(nil), true
}

func seedCommit(seed uint32, salt string) string {
	if salt == "" {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write([]byte(salt))
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], seed)
	_, _ = h.Write(buf[:])
	return hex.EncodeToString(h.Sum(nil))
}

func buildSlicePayload(manifest game.SliceManifest, revealSalt string, uid int64) slicePayload {
	userSeed := game.UserSeed(manifest.Seed, uid)
	outcome := game.BuildSliceRuntimeWithSeed(manifest, userSeed)
	visualSeed := game.UserVisualSeed(manifest.Seed, uid, revealSalt)
	runtime := game.BuildSliceRuntimeWithSeeds(manifest, userSeed, visualSeed)
	dropTypes := make([]int, manifest.DropCount)
	for i := 0; i < manifest.DropCount; i++ {
		if i < len(outcome.IsBomb) && outcome.IsBomb[i] {
			dropTypes[i] = 1
			continue
		}
		if i < len(outcome.IsEmpty) && outcome.IsEmpty[i] {
			dropTypes[i] = 3
			continue
		}
		if i < len(outcome.IsBig) && outcome.IsBig[i] {
			dropTypes[i] = 2
			continue
		}
		dropTypes[i] = 0
	}
	return slicePayload{
		SliceID:       manifest.SliceID,
		StartAtMS:     manifest.StartAtMS,
		DurationMS:    manifest.DurationMS,
		DropCount:     manifest.DropCount,
		BombCount:     manifest.BombCount,
		BigCount:      manifest.BigCount,
		EmptyCount:    manifest.EmptyCount,
		BigMultiplier: manifest.BigMultiplier,
		WindowMS:      manifest.WindowMS,
		ScoreTotal:    manifest.ScoreTotal,
		OffsetsMS:     runtime.OffsetsMS,
		DropTypes:     dropTypes,
		SeedCommit:    seedCommit(userSeed, revealSalt),
	}
}
