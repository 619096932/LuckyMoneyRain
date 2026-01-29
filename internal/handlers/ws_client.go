package handlers

import (
	"github.com/gorilla/websocket"
)

type WSClient struct {
	UserID int64
	Conn   *websocket.Conn
	SendCh chan []byte
}

func NewWSClient(userID int64, conn *websocket.Conn) *WSClient {
	return &WSClient{
		UserID: userID,
		Conn:   conn,
		SendCh: make(chan []byte, 32),
	}
}

func (c *WSClient) Send(payload []byte) {
	select {
	case c.SendCh <- payload:
	default:
		// 如果缓冲满，直接丢弃避免阻塞
	}
}

func (c *WSClient) WritePump() {
	for msg := range c.SendCh {
		if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}
