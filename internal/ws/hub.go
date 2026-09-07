package ws

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/filetransfer/backend/internal/models"
	"github.com/gorilla/websocket"
)

// Hub maintains active WebSocket connections and routes messages.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client // userID -> client
}

type Client struct {
	UserID string
	Conn   *websocket.Conn
	Hub    *Hub
	Send   chan []byte
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
	}
}

func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close existing connection if any (single device per user)
	if existing, ok := h.clients[client.UserID]; ok {
		existing.Conn.Close()
	}
	h.clients[client.UserID] = client
	log.Printf("User %s connected", client.UserID)
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.clients[client.UserID]; ok && existing == client {
		delete(h.clients, client.UserID)
		close(client.Send)
		log.Printf("User %s disconnected", client.UserID)
	}
}

func (h *Hub) IsOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[userID]
	return ok
}

// SendToUser sends a WebSocket message to a specific user if they're online.
// Returns true if the message was sent, false if user is offline.
func (h *Hub) SendToUser(userID string, msg models.WSMessage) bool {
	h.mu.RLock()
	client, ok := h.clients[userID]
	h.mu.RUnlock()

	if !ok {
		return false
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal WS message: %v", err)
		return false
	}

	select {
	case client.Send <- data:
		return true
	default:
		// Buffer full, drop connection
		h.Unregister(client)
		return false
	}
}

// WritePump sends messages from the Send channel to the WebSocket connection.
func (c *Client) WritePump() {
	defer c.Conn.Close()
	for msg := range c.Send {
		if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("Write error for user %s: %v", c.UserID, err)
			return
		}
	}
}
