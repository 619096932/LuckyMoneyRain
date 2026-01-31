package handlers

import (
	"context"
	"encoding/hex"
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
	signKey := ""
	if key, ok := s.gameSignKey(claims.SessionID); ok {
		signKey = hex.EncodeToString(key)
	}
	_ = conn.WriteMessage(websocket.TextMessage, mustJSON(WSMessage{
		Type: "hello",
		Data: map[string]interface{}{
			"server_time": time.Now().UnixMilli(),
			"sign_key":    signKey,
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
			Data: roundStatePayload(payloadRound, current.Slices, current.RevealSalt, &eligible, onlineCount, int(whitelistCount), claims.UserID),
		}))
	}

	// 读循环(仅用于保持连接)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var inbound struct {
			Type string          `json:"type"`
			Ts   int64           `json:"ts"`
			Seq  int64           `json:"seq"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &inbound); err != nil {
			continue
		}
		switch inbound.Type {
		case "ping":
			s.MarkOnline(claims.UserID)
			client.Send(mustJSON(WSMessage{
				Type: "pong",
				Data: map[string]interface{}{
					"ts":          inbound.Ts,
					"seq":         inbound.Seq,
					"server_time": time.Now().UnixMilli(),
				},
			}))
		case "click", "c":
			var req clickRequest
			if len(inbound.Data) > 0 {
				var short struct {
					R int64  `json:"r"`
					D int    `json:"d"`
					T int64  `json:"t"`
					S string `json:"s"`
					Seq int64 `json:"seq"`
				}
				if err := json.Unmarshal(inbound.Data, &short); err == nil && short.R > 0 {
					req.RoundID = short.R
					req.DropID = short.D
					req.ClientTS = short.T
					req.Sign = short.S
					if inbound.Seq == 0 && short.Seq > 0 {
						inbound.Seq = short.Seq
					}
				} else {
					_ = json.Unmarshal(inbound.Data, &req)
				}
			} else {
				_ = json.Unmarshal(msg, &req)
			}
			if req.RoundID <= 0 {
				respType := "click_result"
				if inbound.Type == "c" {
					respType = "cr"
				}
				client.Send(mustJSON(WSMessage{
					Type: respType,
					Data: map[string]interface{}{
						"e": "invalid request",
					},
				}))
				continue
			}
			s.MarkOnline(claims.UserID)
			if !s.verifySign(claims.UserID, claims.SessionID, req.RoundID, req.DropID, req.ClientTS, req.Sign) {
				respType := "click_result"
				if inbound.Type == "c" {
					respType = "cr"
				}
				client.Send(mustJSON(WSMessage{
					Type: respType,
					Data: map[string]interface{}{
						"s": inbound.Seq,
						"r": req.RoundID,
						"d": req.DropID,
						"e": "invalid sign",
					},
				}))
				continue
			}
			delta, total, isBomb, err := s.processClick(context.Background(), claims.UserID, req.RoundID, req.DropID, req.ClientTS)
			if err != nil {
				respType := "click_result"
				if inbound.Type == "c" {
					respType = "cr"
				}
				client.Send(mustJSON(WSMessage{
					Type: respType,
					Data: map[string]interface{}{
						"s": inbound.Seq,
						"r": req.RoundID,
						"d": req.DropID,
						"e": err.Error(),
					},
				}))
				continue
			}
			respType := "click_result"
			if inbound.Type == "c" {
				respType = "cr"
			}
			if respType == "cr" {
				client.Send(mustJSON(WSMessage{
					Type: "cr",
					Data: map[string]interface{}{
						"s": inbound.Seq,
						"r": req.RoundID,
						"d": req.DropID,
						"v": delta,
						"t": total,
						"b": boolToInt(isBomb),
					},
				}))
			} else {
				client.Send(mustJSON(WSMessage{
					Type: "click_result",
					Data: map[string]interface{}{
						"round_id": req.RoundID,
						"drop_id":  req.DropID,
						"delta":    delta,
						"total":    total,
						"bomb":     isBomb,
					},
				}))
			}
		}
	}
}

func roundStatePayload(round models.Round, slices []game.SliceRuntime, revealSalt string, eligible *bool, onlineCount int, whitelistCount int, userID int64) map[string]interface{} {
	resp := map[string]interface{}{
		"round":       round,
		"server_time": time.Now().UnixMilli(),
	}
	if eligible != nil {
		resp["eligible"] = *eligible
	}
	resp["online_count"] = onlineCount
	resp["whitelist_count"] = whitelistCount
	if userID > 0 && (eligible == nil || *eligible) && (round.Status == models.RoundRunning || round.Status == models.RoundCountdown || round.Status == models.RoundLocked) {
		manifests := make([]slicePayload, 0, len(slices))
		for _, s := range slices {
			manifests = append(manifests, buildSlicePayload(s.Manifest, revealSalt, userID))
		}
		resp["slices"] = manifests
	}
	return resp
}

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
