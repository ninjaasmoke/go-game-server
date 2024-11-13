package types

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Room struct represents a chat room
type Room struct {
	ID      string
	Players map[string]Player
	sync.RWMutex
	LastActivity time.Time // Track the last activity time for cleanup
}

// Message struct represents a chat message
type Message struct {
	From    string `json:"from"`
	Content string `json:"content"`
	RoomID  string `json:"roomID"`
	Type    string `json:"type"`
	Level   int16  `json:"level"`
	ID      string `json:"id"`
}

// Player type
type Player struct {
	Connection *websocket.Conn
	UUID       string `json:"uuid"`
	IsTurn     bool   `json:"isTurn"`
	PlayingAs  string `json:"playingAs"`
}
