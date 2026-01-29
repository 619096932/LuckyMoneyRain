package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"hongbao/internal/auth"
	"hongbao/internal/game"
	"hongbao/internal/models"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = getBearerToken(r)
	}
	claims, err := auth.ParseToken(s.JWTSecret, token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := s.validateSession(claims.UserID, claims.SessionID); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := NewWSClient(claims.UserID, conn)
	s.Hub.Register(client)
	s.MarkOnline(claims.UserID)
	defer func() {
		s.Hub.Unregister(client)
		_ = conn.Close()
		close(client.SendCh)
	}()

	go client.WritePump()

	// 发送 hello + 当前轮次状态
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.WriteMessage(websocket.TextMessage, mustJSON(WSMessage{
		Type: "hello",
		Data: map[string]interface{}{
			"server_time": time.Now().UnixMilli(),
			"user": map[string]interface{}{
				"id":    claims.UserID,
				"phone": claims.Phone,
			},
		},
	}))

	current := s.Game.GetCurrent()
	if current != nil {
		eligible := s.isWhitelisted(current.Round.ID, claims.UserID)
		payloadRound := current.Round
		if !eligible && payloadRound.Status != models.RoundWaiting && payloadRound.Status != models.RoundLocked {
			payloadRound.Status = models.RoundLocked
		}
		whitelistCount, _ := s.Redis.SCard(context.Background(), whitelistKey(current.Round.ID)).Result()
		onlineCount := len(s.getActiveOnlineUserIDs(context.Background()))
		_ = conn.WriteMessage(websocket.TextMessage, mustJSON(WSMessage{
			Type: "round_state",
			Data: roundStatePayload(payloadRound, current.Slices, &eligible, onlineCount, int(whitelistCount), claims.UserID),
		}))
	}

	// 读循环(仅用于保持连接)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var inbound struct {
			Type string `json:"type"`
			Ts   int64  `json:"ts"`
		}
		if err := json.Unmarshal(msg, &inbound); err == nil {
			if inbound.Type == "ping" {
				s.MarkOnline(claims.UserID)
			}
		}
	}
}

func roundStatePayload(round models.Round, slices []game.SliceRuntime, eligible *bool, onlineCount int, whitelistCount int, userID int64) map[string]interface{} {
	resp := map[string]interface{}{
		"round":       round,
		"server_time": time.Now().UnixMilli(),
	}
	if eligible != nil {
		resp["eligible"] = *eligible
	}
	resp["online_count"] = onlineCount
	resp["whitelist_count"] = whitelistCount
	if (eligible == nil || *eligible) && (round.Status == models.RoundRunning || round.Status == models.RoundCountdown || round.Status == models.RoundLocked) {
		manifests := make([]game.SliceManifest, 0, len(slices))
		for _, s := range slices {
			manifest := s.Manifest
			if userID > 0 {
				manifest.Seed = game.UserSeed(manifest.Seed, userID)
			}
			manifests = append(manifests, manifest)
		}
		resp["slices"] = manifests
	}
	return resp
}

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
