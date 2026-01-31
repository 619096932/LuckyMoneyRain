package handlers

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

func (s *Server) bumpQPS(ctx context.Context, roundID int64, nowMS int64) error {
	if s == nil {
		return nil
	}
	val, _ := s.qpsCounters.LoadOrStore(roundID, &atomic.Int64{})
	counter := val.(*atomic.Int64)
	counter.Add(1)
	return nil
}

func (s *Server) startQPSFlusher() {
	if s == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.flushQPS()
		}
	}()
}

func (s *Server) flushQPS() {
	if s == nil || s.Redis == nil {
		return
	}
	nowSec := time.Now().Unix()
	ctx := context.Background()
	pipe := s.Redis.Pipeline()
	has := false
	s.qpsCounters.Range(func(key, value any) bool {
		roundID, ok := key.(int64)
		if !ok {
			return true
		}
		counter, ok := value.(*atomic.Int64)
		if !ok {
			return true
		}
		n := counter.Swap(0)
		if n <= 0 {
			return true
		}
		has = true
		redisKey := fmt.Sprintf("round:%d:qps:%d", roundID, nowSec)
		pipe.IncrBy(ctx, redisKey, n)
		pipe.Expire(ctx, redisKey, 10*time.Second)
		return true
	})
	if has {
		_, _ = pipe.Exec(ctx)
	}
}
