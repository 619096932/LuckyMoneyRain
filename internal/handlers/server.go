package handlers

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"hongbao/internal/auth"
	"hongbao/internal/config"
	"hongbao/internal/game"
	"hongbao/internal/payments"
	"hongbao/internal/sms"
)

type Server struct {
	Cfg             config.Config
	DB              *sql.DB
	Redis           *redis.Client
	Game            *game.Manager
	SMS             *sms.SubmailClient
	JWTSecret       []byte
	Hub             *Hub
	Alipay          *payments.AlipayClient
	withdrawEnabled atomic.Bool
	onlineTouch     sync.Map
	qpsCounters     sync.Map
}

func NewServer(cfg config.Config, db *sql.DB, redis *redis.Client) *Server {
	alipayClient, err := payments.NewAlipayClient(payments.AlipayConfig{
		AppID:                    cfg.AlipayAppID,
		PrivateKey:               cfg.AlipayPrivateKey,
		AppCertPath:              cfg.AlipayAppCertPath,
		AlipayCertPath:           cfg.AlipayAlipayCertPath,
		AlipayRootCertPath:       cfg.AlipayRootCertPath,
		Env:                      cfg.AlipayEnv,
		IdentityType:             cfg.AlipayIdentityType,
		BizScene:                 cfg.AlipayBizScene,
		ProductCode:              cfg.AlipayProductCode,
		OrderTitle:               cfg.AlipayOrderTitle,
		Remark:                   cfg.AlipayRemark,
		TransferSceneName:        cfg.AlipayTransferSceneName,
		TransferSceneReportInfos: cfg.AlipayTransferSceneReportInfos,
	})
	if err != nil {
		log.Printf("alipay init error: %v", err)
	}
	srv := &Server{
		Cfg:       cfg,
		DB:        db,
		Redis:     redis,
		Game:      game.NewManager(redis, cfg.ClickWindowMS, cfg.MinSpeedMult, cfg.TimeSkewMS, cfg.ClickGraceMS, cfg.RuntimeCacheUsers, cfg.RuntimeCacheSlices),
		SMS:       sms.NewSubmailClient(cfg.SubmailAppID, cfg.SubmailAppKey, cfg.SubmailProjectID),
		JWTSecret: []byte(cfg.JWTSecret),
		Hub:       NewHub(),
		Alipay:    alipayClient,
	}
	srv.withdrawEnabled.Store(cfg.WithdrawEnabled)
	srv.loadWithdrawSwitch()
	srv.startQPSFlusher()
	return srv
}

func (s *Server) SignToken(userID int64, phone string, isAdmin bool) (string, error) {
	sessionID := newSessionID()
	if err := s.saveSession(userID, sessionID, 7*24*time.Hour); err != nil {
		return "", err
	}
	return auth.GenerateToken(s.JWTSecret, userID, phone, isAdmin, sessionID, 7*24*time.Hour)
}

func (s *Server) SignAdminToken() (string, error) {
	sessionID := newSessionID()
	if err := s.saveSession(0, sessionID, 8*time.Hour); err != nil {
		return "", err
	}
	return auth.GenerateToken(s.JWTSecret, 0, "admin", true, sessionID, 8*time.Hour)
}

func (s *Server) MarkOnline(userID int64) {
	if s.Redis == nil {
		return
	}
	if userID <= 0 {
		return
	}
	now := time.Now().UnixMilli()
	if val, ok := s.onlineTouch.Load(userID); ok {
		if last, ok := val.(int64); ok && now-last < 2000 {
			return
		}
	}
	s.onlineTouch.Store(userID, now)
	_ = s.Redis.HSet(context.Background(), onlineUsersKey(), userID, now).Err()
	_ = s.Redis.SAdd(context.Background(), onlineUserIDsKey(), userID).Err()
}

func (s *Server) isWhitelisted(roundID int64, userID int64) bool {
	if s.Redis == nil {
		return s.isWhitelistedDB(roundID, userID)
	}
	ok, _ := s.Redis.SIsMember(context.Background(), whitelistKey(roundID), userID).Result()
	if ok {
		return true
	}
	// fallback: DB 校验并补写 Redis
	if s.isWhitelistedDB(roundID, userID) {
		_ = s.Redis.SAdd(context.Background(), whitelistKey(roundID), userID).Err()
		return true
	}
	return false
}

func (s *Server) isWhitelistedDB(roundID int64, userID int64) bool {
	row := s.DB.QueryRow(`SELECT 1 FROM round_whitelist WHERE round_id=? AND user_id=? LIMIT 1`, roundID, userID)
	var one int
	if err := row.Scan(&one); err != nil {
		return false
	}
	return true
}

func (s *Server) saveSession(userID int64, sessionID string, ttl time.Duration) error {
	if s.Redis == nil {
		return nil
	}
	key := sessionKey(userID)
	return s.Redis.Set(context.Background(), key, sessionID, ttl).Err()
}

func (s *Server) validateSession(userID int64, sessionID string) error {
	if s.Redis == nil {
		return nil
	}
	key := sessionKey(userID)
	val, err := s.Redis.Get(context.Background(), key).Result()
	if err != nil {
		if err == redis.Nil {
			return errInvalidSession
		}
		return err
	}
	if val != sessionID {
		return errInvalidSession
	}
	return nil
}

func (s *Server) roundKeyTTL(roundID int64) time.Duration {
	rt := s.Game.GetCurrent()
	if rt != nil && rt.Round.ID == roundID && rt.Round.EndAtMS > 0 {
		ttl := time.Until(time.UnixMilli(rt.Round.EndAtMS).Add(2 * time.Hour))
		if ttl < time.Minute {
			return 2 * time.Hour
		}
		return ttl
	}
	return 2 * time.Hour
}
