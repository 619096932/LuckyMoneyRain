package handlers

import (
	"sync"
)

type Hub struct {
	mu       sync.RWMutex
	clients  map[int64]map[*WSClient]bool
	broadcast chan []byte
}

func NewHub() *Hub {
	return &Hub{
		clients:  make(map[int64]map[*WSClient]bool),
		broadcast: make(chan []byte, 128),
	}
}

func (h *Hub) Register(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.UserID] == nil {
		h.clients[client.UserID] = make(map[*WSClient]bool)
	}
	h.clients[client.UserID][client] = true
}

func (h *Hub) Unregister(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.UserID] != nil {
		delete(h.clients[client.UserID], client)
		if len(h.clients[client.UserID]) == 0 {
			delete(h.clients, client.UserID)
		}
	}
}

func (h *Hub) Broadcast(payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, set := range h.clients {
		for client := range set {
			client.Send(payload)
		}
	}
}

func (h *Hub) SendToUser(userID int64, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients[userID] {
		client.Send(payload)
	}
}

func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) UserIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]int64, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}
