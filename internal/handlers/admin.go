package handlers

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"hongbao/internal/game"
	"hongbao/internal/models"
)

type createRoundRequest struct {
	Title         string  `json:"title"`
	TotalPool     int64   `json:"total_pool"`
	DurationSec   int     `json:"duration_sec"`
	SliceMS       int     `json:"slice_ms"`
	DropsPerSlice int     `json:"drops_per_slice"`
	BombsPerSlice int     `json:"bombs_per_slice"`
	BigsPerSlice  int     `json:"bigs_per_slice"`
	EmptyPerSlice int     `json:"empty_per_slice"`
	BigMultiplier float64 `json:"big_multiplier"`
	MaxSpeed      float64 `json:"max_speed"`
	DropVisibleMS int     `json:"drop_visible_ms"`
	ScoreTotal    int     `json:"score_total"`
	BombPenalty   int     `json:"bomb_penalty"`
	MinAward      int64   `json:"min_award"`
	MaxAward      int64   `json:"max_award"`
	LuckyRatio    int     `json:"lucky_ratio"`
	BaseRatio     int     `json:"base_ratio"`
	TailTopN      int     `json:"tail_top_n"`
	RankSegments  int     `json:"rank_segments"`
}

type whitelistRequest struct {
	Phones  []string `json:"phones"`
	UserIDs []int64  `json:"user_ids"`
}

type startRoundRequest struct {
	CountdownSec int `json:"countdown_sec"`
}

type adminLoginRequest struct {
	Password string `json:"password"`
}

func (s *Server) AdminLogin(c *gin.Context) {
	var req adminLoginRequest
	_ = c.ShouldBindJSON(&req)
	if req.Password == "" {
		req.Password = strings.TrimSpace(c.PostForm("password"))
	}
	if req.Password == "" {
		req.Password = strings.TrimSpace(c.Query("password"))
	}
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required"})
		return
	}
	if strings.TrimSpace(s.Cfg.AdminPassword) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin password not configured"})
		return
	}
	if s.Cfg.AdminPassword != req.Password {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}
	token, err := s.SignAdminToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func (s *Server) CreateRound(c *gin.Context) {
	var req createRoundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.DurationSec <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration required"})
		return
	}
	if req.TotalPool <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "total_pool required"})
		return
	}
	// 简化设置：未填字段使用默认/智能值
	durationMS := req.DurationSec * 1000
	if req.SliceMS <= 0 {
		req.SliceMS = 1000
	}
	if durationMS > 0 && req.SliceMS > durationMS {
		req.SliceMS = durationMS
	}
	if req.DropsPerSlice <= 0 {
		// 默认每秒 6 个红包，提升密度
		sliceCount := req.DurationSec * 1000 / req.SliceMS
		if sliceCount <= 0 {
			sliceCount = req.DurationSec
		}
		totalDrops := req.DurationSec * 6
		if totalDrops < req.DurationSec*4 {
			totalDrops = req.DurationSec * 4
		}
		drops := totalDrops / sliceCount
		if drops <= 0 {
			drops = 4
		}
		if drops > 12 {
			drops = 12
		}
		if req.BombsPerSlice > 0 && drops <= req.BombsPerSlice {
			drops = req.BombsPerSlice + 1
		}
		req.DropsPerSlice = drops
	}
	// BombsPerSlice: 只在值为 -1（未设置）时才使用默认值，0 表示无炸弹
	if req.BombsPerSlice < 0 {
		bombs := int(float64(req.DropsPerSlice) * 0.2)
		if bombs <= 0 {
			bombs = 1
		}
		if bombs >= req.DropsPerSlice {
			bombs = req.DropsPerSlice - 1
		}
		req.BombsPerSlice = bombs
	}
	if req.BigsPerSlice < 0 {
		req.BigsPerSlice = 0
	}
	if req.EmptyPerSlice < 0 {
		req.EmptyPerSlice = 0
	}
	if req.BigMultiplier <= 1 {
		req.BigMultiplier = 2
	}
	if req.MaxSpeed <= 0 {
		req.MaxSpeed = 1.0
	}
	if req.DropVisibleMS < 0 {
		req.DropVisibleMS = 0
	}
	if req.ScoreTotal <= 0 {
		req.ScoreTotal = 1000
	}
	// BombPenalty: 只在值为 -1（未设置）时才使用默认值，0 表示无惩罚
	if req.BombPenalty < 0 {
		req.BombPenalty = 50
	}
	if req.LuckyRatio < 0 {
		req.LuckyRatio = 0
	}
	if req.BaseRatio < 0 {
		req.BaseRatio = 0
	}
	if req.LuckyRatio == 0 && req.BaseRatio == 0 {
		req.LuckyRatio = 40
		req.BaseRatio = 60
	}
	if req.LuckyRatio+req.BaseRatio > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lucky_ratio + base_ratio must be <= 100"})
		return
	}
	if req.TailTopN <= 0 {
		req.TailTopN = 3
	}
	if req.RankSegments <= 0 {
		req.RankSegments = 10
	}
	if req.BombsPerSlice >= req.DropsPerSlice {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid bomb config"})
		return
	}
	if req.BigsPerSlice > req.DropsPerSlice-req.BombsPerSlice {
		req.BigsPerSlice = req.DropsPerSlice - req.BombsPerSlice
		if req.BigsPerSlice < 0 {
			req.BigsPerSlice = 0
		}
	}
	if req.EmptyPerSlice > req.DropsPerSlice-req.BombsPerSlice-req.BigsPerSlice {
		req.EmptyPerSlice = req.DropsPerSlice - req.BombsPerSlice - req.BigsPerSlice
		if req.EmptyPerSlice < 0 {
			req.EmptyPerSlice = 0
		}
	}
	res, err := s.DB.Exec(`INSERT INTO rounds
		(title, total_pool, duration_sec, slice_ms, drops_per_slice, bombs_per_slice, bigs_per_slice, empty_per_slice, big_multiplier, max_speed, drop_visible_ms, score_total, bomb_penalty, min_award, max_award, lucky_ratio, base_ratio, tail_top_n, rank_segments, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		req.Title, req.TotalPool, req.DurationSec, req.SliceMS, req.DropsPerSlice, req.BombsPerSlice, req.BigsPerSlice, req.EmptyPerSlice, req.BigMultiplier, req.MaxSpeed, req.DropVisibleMS, req.ScoreTotal, req.BombPenalty, req.MinAward, req.MaxAward, req.LuckyRatio, req.BaseRatio, req.TailTopN, req.RankSegments, models.RoundWaiting)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(http.StatusOK, gin.H{"id": id})
}

func (s *Server) AddWhitelist(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req whitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	userIDs := make([]int64, 0)
	for _, phone := range req.Phones {
		phone = strings.TrimSpace(phone)
		if phone == "" {
			continue
		}
		user, err := s.getOrCreateUser(phone)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "user error"})
			return
		}
		userIDs = append(userIDs, user.ID)
	}
	userIDs = append(userIDs, req.UserIDs...)
	if len(userIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty whitelist"})
		return
	}

	for _, uid := range userIDs {
		_, err := s.DB.Exec(`INSERT IGNORE INTO round_whitelist (round_id, user_id, created_at) VALUES (?, ?, NOW())`, roundID, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
	}
	// 如果轮次已锁定或正在进行，立即同步到 Redis 白名单
	if round, _ := s.getRoundByID(roundID); round != nil {
		if round.Status != models.RoundWaiting {
			ctx := context.Background()
			for _, uid := range userIDs {
				_ = s.Redis.SAdd(ctx, whitelistKey(roundID), uid).Err()
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "count": len(userIDs)})
}

func (s *Server) LockRound(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.setRoundStatus(roundID, models.RoundLocked); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	// 将当前在线用户纳入白名单 (批量插入优化)
	ctx := context.Background()
	activeIDs := s.getActiveOnlineUserIDs(ctx)

	// 批量 MySQL INSERT (每批 100 条)
	if len(activeIDs) > 0 {
		batchSize := 100
		for i := 0; i < len(activeIDs); i += batchSize {
			end := i + batchSize
			if end > len(activeIDs) {
				end = len(activeIDs)
			}
			batch := activeIDs[i:end]
			if len(batch) == 0 {
				continue
			}
			// 构建批量 INSERT 语句
			query := "INSERT IGNORE INTO round_whitelist (round_id, user_id, created_at) VALUES "
			vals := make([]interface{}, 0, len(batch)*2)
			for j, uid := range batch {
				if j > 0 {
					query += ","
				}
				query += "(?, ?, NOW())"
				vals = append(vals, roundID, uid)
			}
			_, _ = s.DB.Exec(query, vals...)
		}
	}

	// 导入白名单到 Redis (批量 SAdd)
	rows, err := s.DB.Query(`SELECT user_id FROM round_whitelist WHERE round_id = ?`, roundID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()

	key := whitelistKey(roundID)
	members := make([]interface{}, 0)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err == nil {
			members = append(members, uid)
		}
	}
	// 一次性批量 SAdd
	if len(members) > 0 {
		_ = s.Redis.SAdd(ctx, key, members...).Err()
	}

	// 清屏指令：锁定后要求所有端回到等待
	s.broadcastClearScreen(roundID, "locked")
	// 广播状态
	if round, _ := s.getRoundByID(roundID); round != nil {
		s.broadcastRoundState(*round)
	}
	c.JSON(http.StatusOK, gin.H{"status": "locked"})
}

func (s *Server) StartRound(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req startRoundRequest
	_ = c.ShouldBindJSON(&req)
	if req.CountdownSec <= 0 {
		req.CountdownSec = 3
	}

	round, err := s.getRoundByID(roundID)
	if err != nil || round == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "round not found"})
		return
	}
	if round.Status != models.RoundLocked {
		c.JSON(http.StatusBadRequest, gin.H{"error": "round not locked"})
		return
	}
	s.clearRoundCache(roundID)
	seed := randomUint32()
	startAt := time.Now().Add(time.Duration(req.CountdownSec) * time.Second).UnixMilli()
	endAt := startAt + int64(round.DurationSec*1000)

	_, err = s.DB.Exec(`UPDATE rounds SET status=?, start_at_ms=?, end_at_ms=?, seed=?, updated_at=NOW() WHERE id=?`, models.RoundCountdown, startAt, endAt, seed, roundID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	updated, _ := s.getRoundByID(roundID)
	if updated == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "round load error"})
		return
	}
	updated.Seed = seed
	updated.StartAtMS = startAt
	updated.EndAtMS = endAt

	rt, err := game.BuildRoundRuntime(*updated, s.Game.WindowMS())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.Game.SetCurrent(rt)
	s.broadcastRoundState(*updated)

	// 到点切换为 RUNNING
	time.AfterFunc(time.Until(time.UnixMilli(startAt)), func() {
		_ = s.setRoundStatus(roundID, models.RoundRunning)
		if round, _ := s.getRoundByID(roundID); round != nil {
			round.Seed = seed
			round.StartAtMS = startAt
			round.EndAtMS = endAt
			if current := s.Game.GetCurrent(); current != nil && current.Round.ID == roundID {
				current.Round.Status = models.RoundRunning
				s.Game.SetCurrent(current)
			}
			s.broadcastRoundState(*round)
		}
	})

	// 游戏结束自动开奖
	time.AfterFunc(time.Until(time.UnixMilli(endAt)), func() {
		_ = s.setRoundStatus(roundID, models.RoundReadyDraw)
		if round, _ := s.getRoundByID(roundID); round != nil {
			if current := s.Game.GetCurrent(); current != nil && current.Round.ID == roundID {
				current.Round.Status = models.RoundReadyDraw
				s.Game.SetCurrent(current)
			}
			s.broadcastRoundState(*round)
		}
	})

	c.JSON(http.StatusOK, gin.H{"status": "countdown", "start_at": startAt})
}

func (s *Server) DrawRound(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.DrawRoundByID(roundID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "drawn"})
}

func (s *Server) DrawRoundByID(roundID int64) error {
	ctx := context.Background()

	// [FIX-5] 并发安全：添加分布式锁防止重复开奖
	lockKey := fmt.Sprintf("draw_lock:round:%d", roundID)
	locked, err := s.Redis.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
	if err != nil {
		return fmt.Errorf("redis lock error: %w", err)
	}
	if !locked {
		return errors.New("开奖正在进行中，请勿重复操作")
	}
	defer s.Redis.Del(ctx, lockKey)

	round, err := s.getRoundByID(roundID)
	if err != nil || round == nil {
		return errors.New("round not found")
	}
	if round.Status == models.RoundPendingConfirm || round.Status == models.RoundFinished {
		return nil
	}
	if round.Status != models.RoundReadyDraw && round.Status != models.RoundRunning {
		return errors.New("round not ready for draw")
	}

	_ = s.setRoundStatus(roundID, models.RoundDrawing)
	if rt := s.Game.GetCurrent(); rt != nil && rt.Round.ID == roundID {
		rt.Round.Status = models.RoundDrawing
		s.Game.SetCurrent(rt)
	}
	if round != nil {
		round.Status = models.RoundDrawing
		s.broadcastRoundState(*round)
	}

	scores, err := s.Redis.ZRangeWithScores(ctx, scoreZSetKey(roundID), 0, -1).Result()
	if err != nil {
		return err
	}

	scoreMap := make(map[int64]int)
	for _, sc := range scores {
		uid := parseUserID(sc.Member)
		val := int(sc.Score)
		if val < 0 {
			val = 0
		}
		scoreMap[uid] = val
	}

	type alloc struct {
		UserID      int64
		Score       int
		Amount      int64
		BaseAmount  int64
		LuckyAmount int64
		baseFrac    float64 // 用于最大余数法
		luckyFrac   float64 // 用于最大余数法
	}
	allocs := make([]alloc, 0, len(scoreMap))
	for uid, sc := range scoreMap {
		if sc <= 0 {
			continue
		}
		allocs = append(allocs, alloc{UserID: uid, Score: sc})
	}

	alpha := 1.4
	luckyRatio := round.LuckyRatio
	baseRatio := round.BaseRatio
	if luckyRatio < 0 {
		luckyRatio = 0
	}
	if baseRatio < 0 {
		baseRatio = 0
	}
	if luckyRatio == 0 && baseRatio == 0 {
		luckyRatio = 40
		baseRatio = 60
	}

	// [FIX-1 & FIX-6] 修复整数精度丢失和比例和不足100的问题
	// 归一化比例，确保奖池完全分配
	totalRatio := luckyRatio + baseRatio
	if totalRatio <= 0 {
		totalRatio = 100
		luckyRatio = 40
		baseRatio = 60
	}
	// luckyPool 先算，basePool 取剩余，避免精度丢失
	luckyPool := round.TotalPool * int64(luckyRatio) / int64(totalRatio)
	basePool := round.TotalPool - luckyPool

	weights := make([]float64, len(allocs))
	totalWeight := 0.0
	for i, a := range allocs {
		if a.Score <= 0 {
			continue
		}
		w := math.Pow(float64(a.Score), alpha)
		weights[i] = w
		totalWeight += w
	}

	// [FIX-2] 使用最大余数法分配基础池，避免浮点累积误差
	if totalWeight > 0 && basePool > 0 {
		baseAllocated := int64(0)
		for i := range allocs {
			if allocs[i].Score <= 0 {
				continue
			}
			exactAmount := float64(basePool) * weights[i] / totalWeight
			floorAmount := int64(exactAmount)
			allocs[i].BaseAmount = floorAmount
			allocs[i].baseFrac = exactAmount - float64(floorAmount)
			baseAllocated += floorAmount
		}
		// 最大余数法：将剩余金额分配给小数部分最大的用户
		baseDiff := basePool - baseAllocated
		if baseDiff > 0 {
			// 按小数部分降序排序的索引
			idxs := make([]int, len(allocs))
			for i := range idxs {
				idxs[i] = i
			}
			sort.Slice(idxs, func(a, b int) bool {
				return allocs[idxs[a]].baseFrac > allocs[idxs[b]].baseFrac
			})
			for i := int64(0); i < baseDiff && int(i) < len(idxs); i++ {
				allocs[idxs[i]].BaseAmount += 1
			}
		}
	}

	// [FIX-3] 改进随机种子安全性：混合多个因子增加不可预测性
	if luckyPool > 0 && totalWeight > 0 {
		seed := int64(round.Seed)
		if seed == 0 {
			seed = time.Now().UnixNano()
		}
		// 混合 seed + 当前纳秒时间戳 + 用户数量的质数倍，增加不可预测性
		combinedSeed := seed ^ time.Now().UnixNano() ^ int64(len(allocs)*7919)
		rng := mathrand.New(mathrand.NewSource(combinedSeed))

		luckyWeights := make([]float64, len(allocs))
		totalLuckyWeight := 0.0
		for i := range allocs {
			if allocs[i].Score <= 0 {
				continue
			}
			rw := weights[i] * (0.3 + rng.Float64())
			luckyWeights[i] = rw
			totalLuckyWeight += rw
		}
		if totalLuckyWeight == 0 {
			for i := range allocs {
				if allocs[i].Score <= 0 {
					continue
				}
				luckyWeights[i] = 1
				totalLuckyWeight += 1
			}
		}

		// [FIX-2] 幸运池也使用最大余数法
		if totalLuckyWeight > 0 {
			luckyAllocated := int64(0)
			for i := range allocs {
				if allocs[i].Score <= 0 {
					continue
				}
				exactAmount := float64(luckyPool) * luckyWeights[i] / totalLuckyWeight
				floorAmount := int64(exactAmount)
				allocs[i].LuckyAmount = floorAmount
				allocs[i].luckyFrac = exactAmount - float64(floorAmount)
				luckyAllocated += floorAmount
			}
			// 最大余数法分配剩余
			luckyDiff := luckyPool - luckyAllocated
			if luckyDiff > 0 {
				idxs := make([]int, len(allocs))
				for i := range idxs {
					idxs[i] = i
				}
				sort.Slice(idxs, func(a, b int) bool {
					return allocs[idxs[a]].luckyFrac > allocs[idxs[b]].luckyFrac
				})
				for i := int64(0); i < luckyDiff && int(i) < len(idxs); i++ {
					allocs[idxs[i]].LuckyAmount += 1
				}
			}
		}
	}

	for i := range allocs {
		allocs[i].Amount = allocs[i].BaseAmount + allocs[i].LuckyAmount
	}

	// [FIX-4] 优化尾差补偿：按权重比例分配给所有用户（最大余数法已处理大部分，这里是最终校验）
	allocated := int64(0)
	for _, a := range allocs {
		allocated += a.Amount
	}
	diff := round.TotalPool - allocated
	// 如果仍有尾差（理论上不应该有），按权重比例分配
	if diff > 0 && len(allocs) > 0 {
		// 按权重降序排列
		idxs := make([]int, len(allocs))
		for i := range idxs {
			idxs[i] = i
		}
		sort.Slice(idxs, func(a, b int) bool {
			return weights[idxs[a]] > weights[idxs[b]]
		})
		// 按权重占比轮流分配
		for i := int64(0); diff > 0; i++ {
			idx := idxs[int(i)%len(idxs)]
			allocs[idx].Amount += 1
			diff -= 1
		}
	}

	// 写入数据库
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO award_batches (round_id, total_pool, status, created_at) VALUES (?, ?, ?, NOW())`, roundID, round.TotalPool, models.RoundPendingConfirm)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	batchID, _ := res.LastInsertId()
	stmt, err := tx.Prepare(`INSERT INTO award_details (batch_id, user_id, score, amount, base_amount, lucky_amount, created_at) VALUES (?, ?, ?, ?, ?, ?, NOW())`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, a := range allocs {
		if _, err := stmt.Exec(batchID, a.UserID, a.Score, a.Amount, a.BaseAmount, a.LuckyAmount); err != nil {
			_ = tx.Rollback()
			return err
		}
		// 单个用户推送结果
		payload := mustJSON(WSMessage{Type: "round_drawn", Data: map[string]interface{}{
			"round_id":     roundID,
			"score":        a.Score,
			"amount":       a.Amount,
			"base_amount":  a.BaseAmount,
			"lucky_amount": a.LuckyAmount,
		}})
		s.Hub.SendToUser(a.UserID, payload)
	}
	if _, err := tx.Exec(`UPDATE rounds SET status=?, updated_at=NOW() WHERE id=?`, models.RoundPendingConfirm, roundID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if rt := s.Game.GetCurrent(); rt != nil && rt.Round.ID == roundID {
		rt.Round.Status = models.RoundPendingConfirm
		s.Game.SetCurrent(rt)
	}
	if round, _ := s.getRoundByID(roundID); round != nil {
		s.broadcastRoundState(*round)
	}
	return nil
}

func (s *Server) ClearRound(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	s.broadcastClearScreen(roundID, "manual")
	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

func (s *Server) ConfirmAward(c *gin.Context) {
	batchID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.confirmAwardBatchWithRetry(batchID, 1); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "confirmed"})
}

func (s *Server) confirmAwardBatchWithRetry(batchID int64, retry int) error {
	err := s.confirmAwardBatch(batchID)
	if err == nil {
		return nil
	}
	if retry > 0 && isBadConn(err) {
		return s.confirmAwardBatchWithRetry(batchID, retry-1)
	}
	return err
}

func isBadConn(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	return strings.Contains(err.Error(), "bad connection")
}

func (s *Server) confirmAwardBatch(batchID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	var roundID int64
	var status string
	row := tx.QueryRow(`SELECT round_id, status FROM award_batches WHERE id = ? FOR UPDATE`, batchID)
	if err := row.Scan(&roundID, &status); err != nil {
		_ = tx.Rollback()
		return err
	}
	if status == "CONFIRMED" {
		_ = tx.Commit()
		return nil
	}
	rows, err := tx.Query(`SELECT user_id, amount FROM award_details WHERE batch_id = ?`, batchID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	awards := make([]struct {
		uid    int64
		amount int64
	}, 0)
	for rows.Next() {
		var uid int64
		var amount int64
		if err := rows.Scan(&uid, &amount); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return err
		}
		awards = append(awards, struct {
			uid    int64
			amount int64
		}{uid: uid, amount: amount})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return err
	}
	_ = rows.Close()
	for _, award := range awards {
		_, err = tx.Exec(`INSERT INTO wallets (user_id, balance, updated_at) VALUES (?, ?, NOW())
			ON DUPLICATE KEY UPDATE balance = balance + VALUES(balance), updated_at=NOW()`, award.uid, award.amount)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		_, err = tx.Exec(`INSERT INTO wallet_ledger (user_id, amount, reason, ref_id, created_at) VALUES (?, ?, ?, ?, NOW())`, award.uid, award.amount, "ROUND_AWARD", batchID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE award_batches SET status='CONFIRMED', confirmed_at=NOW() WHERE id=?`, batchID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`UPDATE rounds SET status=?, updated_at=NOW() WHERE id=?`, models.RoundFinished, roundID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if rt := s.Game.GetCurrent(); rt != nil && rt.Round.ID == roundID {
		rt.Round.Status = models.RoundFinished
		s.Game.SetCurrent(rt)
	}
	if round, _ := s.getRoundByID(roundID); round != nil {
		s.broadcastRoundState(*round)
	}
	return nil
}

func (s *Server) ListAwardBatches(c *gin.Context) {
	rows, err := s.DB.Query(`SELECT id, round_id, total_pool, status, created_at FROM award_batches ORDER BY id DESC LIMIT 20`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()
	list := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, roundID int64
		var pool int64
		var status string
		var created time.Time
		if err := rows.Scan(&id, &roundID, &pool, &status, &created); err == nil {
			list = append(list, map[string]interface{}{
				"id": id, "round_id": roundID, "total_pool": pool, "status": status, "created_at": created,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

func (s *Server) ListRounds(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	rows, err := s.DB.Query(`SELECT id, title, total_pool, duration_sec, slice_ms, drops_per_slice, bombs_per_slice, bigs_per_slice, empty_per_slice, big_multiplier, max_speed, drop_visible_ms,
		score_total, bomb_penalty, min_award, max_award, lucky_ratio, base_ratio, tail_top_n, rank_segments, status, start_at_ms, end_at_ms, created_at
		FROM rounds ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var r models.Round
		var status string
		var created time.Time
		if err := rows.Scan(&r.ID, &r.Title, &r.TotalPool, &r.DurationSec, &r.SliceMS, &r.DropsPerSlice, &r.BombsPerSlice, &r.BigsPerSlice, &r.EmptyPerSlice, &r.BigMultiplier, &r.MaxSpeed, &r.DropVisibleMS,
			&r.ScoreTotal, &r.BombPenalty, &r.MinAward, &r.MaxAward, &r.LuckyRatio, &r.BaseRatio, &r.TailTopN, &r.RankSegments, &status, &r.StartAtMS, &r.EndAtMS, &created); err == nil {
			r.Status = models.RoundStatus(status)
			items = append(items, gin.H{
				"id":              r.ID,
				"title":           r.Title,
				"total_pool":      r.TotalPool,
				"duration_sec":    r.DurationSec,
				"slice_ms":        r.SliceMS,
				"drops_per_slice": r.DropsPerSlice,
				"bombs_per_slice": r.BombsPerSlice,
				"bigs_per_slice":  r.BigsPerSlice,
				"empty_per_slice": r.EmptyPerSlice,
				"big_multiplier":  r.BigMultiplier,
				"max_speed":       r.MaxSpeed,
				"drop_visible_ms": r.DropVisibleMS,
				"score_total":     r.ScoreTotal,
				"bomb_penalty":    r.BombPenalty,
				"min_award":       r.MinAward,
				"max_award":       r.MaxAward,
				"lucky_ratio":     r.LuckyRatio,
				"base_ratio":      r.BaseRatio,
				"tail_top_n":      r.TailTopN,
				"rank_segments":   r.RankSegments,
				"status":          r.Status,
				"start_at":        r.StartAtMS,
				"end_at":          r.EndAtMS,
				"created_at":      created,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) ExportRound(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	rows, err := s.DB.Query(`SELECT u.id, u.phone, ad.score, ad.amount, ad.base_amount, ad.lucky_amount
		FROM award_details ad
		JOIN award_batches ab ON ad.batch_id = ab.id
		JOIN users u ON ad.user_id = u.id
		WHERE ab.round_id = ? ORDER BY ad.score DESC`, roundID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer rows.Close()
	filename := fmt.Sprintf("round_%d_export.csv", roundID)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = c.Writer.WriteString("user_id,phone,score,base_amount_fen,lucky_amount_fen,bonus_amount_fen,amount_fen\n")
	for rows.Next() {
		var uid int64
		var phone string
		var score int
		var amount, baseAmount, luckyAmount int64
		if err := rows.Scan(&uid, &phone, &score, &amount, &baseAmount, &luckyAmount); err == nil {
			bonus := amount - baseAmount - luckyAmount
			line := fmt.Sprintf("%d,%s,%d,%d,%d,%d,%d\n", uid, phone, score, baseAmount, luckyAmount, bonus, amount)
			_, _ = c.Writer.WriteString(line)
		}
	}
}

func (s *Server) GetMetrics(c *gin.Context) {
	var round *models.Round
	if idStr := c.Query("round_id"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			round, _ = s.getRoundByID(id)
		}
	} else {
		if rt := s.Game.GetCurrent(); rt != nil {
			r := rt.Round
			round = &r
		}
	}
	now := time.Now().UnixMilli()
	var timeLeft int64
	if round != nil && round.EndAtMS > 0 {
		timeLeft = round.EndAtMS - now
		if timeLeft < 0 {
			timeLeft = 0
		}
	}
	qps := 0
	qps1s := 0
	if round != nil {
		qps, qps1s = s.calcQPS(round.ID, now)
	}
	scoreSum := int64(0)
	scoreUsers := int64(0)
	if round != nil && s.Redis != nil {
		ctx := context.Background()
		var sumErr error
		scoreSum, sumErr = s.Redis.Get(ctx, scoreSumKey(round.ID)).Int64()
		scoreUsers, _ = s.Redis.ZCard(ctx, scoreZSetKey(round.ID)).Result()
		if sumErr == redis.Nil {
			if zs, err := s.Redis.ZRangeWithScores(ctx, scoreZSetKey(round.ID), 0, -1).Result(); err == nil {
				var total int64
				for _, z := range zs {
					total += int64(z.Score)
				}
				scoreSum = total
				_ = s.Redis.Set(ctx, scoreSumKey(round.ID), scoreSum, s.roundKeyTTL(round.ID)).Err()
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"online_users": s.Hub.OnlineCount(),
		"round":        round,
		"time_left_ms": timeLeft,
		"server_time":  now,
		"qps_avg":      qps,
		"qps_1s":       qps1s,
		"score_sum":    scoreSum,
		"score_users":  scoreUsers,
	})
}

func (s *Server) GetLeaderboard(c *gin.Context) {
	roundID, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	limit := 10
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	ctx := context.Background()
	items, err := s.Redis.ZRevRangeWithScores(ctx, scoreZSetKey(roundID), 0, int64(limit-1)).Result()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "redis error"})
		return
	}
	if len(items) == 0 {
		// fallback to award_details
		rows, err := s.DB.Query(`SELECT ad.user_id, ad.score FROM award_details ad
			JOIN award_batches ab ON ad.batch_id = ab.id
			WHERE ab.round_id = ? ORDER BY ad.score DESC LIMIT ?`, roundID, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		defer rows.Close()
		for rows.Next() {
			var uid int64
			var score int
			if err := rows.Scan(&uid, &score); err == nil {
				items = append(items, redis.Z{Member: scoreMember(uid), Score: float64(score)})
			}
		}
	}
	userIDs := make([]int64, 0, len(items))
	for _, item := range items {
		uid := parseUserID(item.Member)
		if uid > 0 {
			userIDs = append(userIDs, uid)
		}
	}
	type userInfo struct {
		Phone     string
		Nickname  string
		AvatarURL string
	}
	infoMap := map[int64]userInfo{}
	if len(userIDs) > 0 {
		query := `SELECT id, phone, nickname, avatar_url FROM users WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(userIDs)), ",") + `)`
		args := make([]interface{}, len(userIDs))
		for i, v := range userIDs {
			args[i] = v
		}
		rows, err := s.DB.Query(query, args...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var phone, nickname, avatarURL string
				if err := rows.Scan(&id, &phone, &nickname, &avatarURL); err == nil {
					infoMap[id] = userInfo{Phone: phone, Nickname: nickname, AvatarURL: avatarURL}
				}
			}
		}
	}
	resp := make([]gin.H, 0, len(items))
	for _, item := range items {
		uid := parseUserID(item.Member)
		info := infoMap[uid]
		resp = append(resp, gin.H{
			"user_id":    uid,
			"phone":      info.Phone,
			"nickname":   info.Nickname,
			"avatar_url": info.AvatarURL,
			"score":      int(item.Score),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": resp})
}

func (s *Server) GetOnlineUsers(c *gin.Context) {
	ctx := context.Background()
	active := s.getActiveOnlineUserIDs(ctx)
	type userInfo struct {
		Phone     string
		Nickname  string
		AvatarURL string
	}
	infoMap := map[int64]userInfo{}
	if len(active) > 0 {
		query := `SELECT id, phone, nickname, avatar_url FROM users WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(active)), ",") + `)`
		args := make([]interface{}, len(active))
		for i, v := range active {
			args[i] = v
		}
		rows, err := s.DB.Query(query, args...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var phone, nickname, avatarURL string
				if err := rows.Scan(&id, &phone, &nickname, &avatarURL); err == nil {
					infoMap[id] = userInfo{Phone: phone, Nickname: nickname, AvatarURL: avatarURL}
				}
			}
		}
	}
	resp := make([]gin.H, 0, len(active))
	for _, uid := range active {
		info := infoMap[uid]
		resp = append(resp, gin.H{
			"user_id":    uid,
			"phone":      info.Phone,
			"nickname":   info.Nickname,
			"avatar_url": info.AvatarURL,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": resp})
}

func (s *Server) getActiveOnlineUserIDs(ctx context.Context) []int64 {
	// 使用 HGetAll 一次性获取所有在线用户时间戳，避免 O(N) 次 Redis 请求
	tsMap, err := s.Redis.HGetAll(ctx, onlineUsersKey()).Result()
	if err != nil {
		return nil
	}
	now := time.Now().UnixMilli()
	active := make([]int64, 0, len(tsMap))
	expired := make([]string, 0)

	for idStr, tsStr := range tsMap {
		uid, _ := strconv.ParseInt(idStr, 10, 64)
		ts, _ := strconv.ParseInt(tsStr, 10, 64)
		if ts > 0 && now-ts <= 20000 {
			active = append(active, uid)
		} else {
			expired = append(expired, idStr)
		}
	}

	// 使用 Pipeline 批量清理过期用户
	if len(expired) > 0 {
		pipe := s.Redis.Pipeline()
		for _, idStr := range expired {
			pipe.SRem(ctx, onlineUserIDsKey(), idStr)
			pipe.HDel(ctx, onlineUsersKey(), idStr)
		}
		_, _ = pipe.Exec(ctx)
	}
	return active
}

func (s *Server) calcQPS(roundID int64, nowMS int64) (int, int) {
	ctx := context.Background()
	sec := nowMS / 1000
	var total int64
	var last int64
	for i := int64(0); i < 5; i++ {
		key := fmt.Sprintf("round:%d:qps:%d", roundID, sec-i)
		val, _ := s.Redis.Get(ctx, key).Int64()
		if i == 0 {
			last = val
		}
		total += val
	}
	return int(total / 5), int(last)
}

func (s *Server) broadcastRoundState(round models.Round) {
	rt := s.Game.GetCurrent()
	var slices []game.SliceRuntime
	if rt != nil && rt.Round.ID == round.ID {
		slices = rt.Slices
	}
	ctx := context.Background()
	onlineCount := len(s.getActiveOnlineUserIDs(ctx))
	whitelistCount, _ := s.Redis.SCard(ctx, whitelistKey(round.ID)).Result()
	userIDs := s.Hub.UserIDs()
	if len(userIDs) == 0 {
		payload := mustJSON(WSMessage{
			Type: "round_state",
			Data: roundStatePayload(round, slices, nil, onlineCount, int(whitelistCount), 0),
		})
		s.Hub.Broadcast(payload)
		return
	}

	// 批量查询白名单状态 (使用 SMIsMember 一次性查询)
	whitelistKey := whitelistKey(round.ID)
	members := make([]interface{}, len(userIDs))
	for i, uid := range userIDs {
		members[i] = uid
	}
	eligibleMap := make(map[int64]bool, len(userIDs))

	// SMIsMember 批量检查 (Redis 6.2+)
	results, err := s.Redis.SMIsMember(ctx, whitelistKey, members...).Result()
	if err == nil && len(results) == len(userIDs) {
		for i, uid := range userIDs {
			eligibleMap[uid] = results[i]
		}
	} else {
		// 回退：使用 Pipeline 批量查询
		pipe := s.Redis.Pipeline()
		cmds := make([]*redis.BoolCmd, len(userIDs))
		for i, uid := range userIDs {
			cmds[i] = pipe.SIsMember(ctx, whitelistKey, uid)
		}
		_, _ = pipe.Exec(ctx)
		for i, uid := range userIDs {
			eligibleMap[uid] = cmds[i].Val()
		}
	}

	// 预生成两种 payload：eligible=true 和 eligible=false
	// 对于 WAITING 和 LOCKED 状态，所有人看到的内容相同
	if round.Status == models.RoundWaiting || round.Status == models.RoundLocked {
		eligibleTrue := true
		payload := mustJSON(WSMessage{
			Type: "round_state",
			Data: roundStatePayload(round, slices, &eligibleTrue, onlineCount, int(whitelistCount), 0),
		})
		s.Hub.Broadcast(payload)
		return
	}

	// 其他状态：按白名单分组发送
	for _, uid := range userIDs {
		eligible := eligibleMap[uid]
		payloadRound := round
		if !eligible {
			payloadRound.Status = models.RoundLocked
		}
		payload := mustJSON(WSMessage{
			Type: "round_state",
			Data: roundStatePayload(payloadRound, slices, &eligible, onlineCount, int(whitelistCount), uid),
		})
		s.Hub.SendToUser(uid, payload)
	}
}

func (s *Server) broadcastClearScreen(roundID int64, reason string) {
	payload := mustJSON(WSMessage{
		Type: "clear_screen",
		Data: map[string]interface{}{
			"round_id": roundID,
			"reason":   reason,
		},
	})
	s.Hub.Broadcast(payload)
}

func (s *Server) clearRoundCache(roundID int64) {
	if s.Redis == nil {
		return
	}
	ctx := context.Background()
	_ = s.Redis.Del(ctx, scoreZSetKey(roundID), scoreSumKey(roundID), clickStreamKey(roundID)).Err()
}

func (s *Server) getRoundByID(id int64) (*models.Round, error) {
	row := s.DB.QueryRow(`SELECT id, title, total_pool, duration_sec, slice_ms, drops_per_slice, bombs_per_slice, bigs_per_slice, empty_per_slice, big_multiplier, max_speed, drop_visible_ms,
		score_total, bomb_penalty, min_award, max_award, lucky_ratio, base_ratio, tail_top_n, rank_segments, status, start_at_ms, end_at_ms, seed, created_at, updated_at
		FROM rounds WHERE id = ?`, id)
	var r models.Round
	var status string
	if err := row.Scan(&r.ID, &r.Title, &r.TotalPool, &r.DurationSec, &r.SliceMS, &r.DropsPerSlice, &r.BombsPerSlice, &r.BigsPerSlice, &r.EmptyPerSlice, &r.BigMultiplier, &r.MaxSpeed, &r.DropVisibleMS,
		&r.ScoreTotal, &r.BombPenalty, &r.MinAward, &r.MaxAward, &r.LuckyRatio, &r.BaseRatio, &r.TailTopN, &r.RankSegments, &status, &r.StartAtMS, &r.EndAtMS, &r.Seed, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Status = models.RoundStatus(status)
	return &r, nil
}

func (s *Server) setRoundStatus(roundID int64, status models.RoundStatus) error {
	_, err := s.DB.Exec(`UPDATE rounds SET status=?, updated_at=NOW() WHERE id=?`, status, roundID)
	return err
}

func parseUserID(member interface{}) int64 {
	s, ok := member.(string)
	if !ok {
		return 0
	}
	var uid int64
	_, _ = fmt.Sscanf(s, "u:%d", &uid)
	return uid
}

func randomUint32() uint32 {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func parseIDParam(c *gin.Context, name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}
