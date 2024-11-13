package main

import (
	"fmt"
	"net/http"
	"sync"
	"tic-tac-toe/types"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	rooms    = make(map[string]*types.Room)
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		// If this is a preflight request, we're done
		if r.Method == "OPTIONS" {
			return
		}

		// Otherwise, proceed with the next handler
		next.ServeHTTP(w, r)
	})
}

func main() {
	fs := http.FileServer(http.Dir("public"))
	http.Handle("/", fs)

	// http.HandleFunc("/create", handleCreateRoom)
	// http.HandleFunc("/ws", handleJoinRoom)
	// http.HandleFunc("/join", handleJoinRoom)
	// http.HandleFunc("/leave", handleLeaveRoom)

	http.Handle("/create", enableCORS(http.HandlerFunc(handleCreateRoom)))
	http.Handle("/ws", enableCORS(http.HandlerFunc(handleJoinRoom)))
	http.Handle("/join", enableCORS(http.HandlerFunc(handleJoinRoom)))
	http.Handle("/leave", enableCORS(http.HandlerFunc(handleLeaveRoom)))

	go cleanupRooms() // Start the room cleanup goroutine

	fmt.Println("Server started on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server: ", err)
	}
}

func handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	roomID := uuid.New().String()
	room := &types.Room{
		ID:      roomID,
		Players: make(map[string]types.Player),
	}
	rooms[roomID] = room
	w.Write([]byte(roomID))
}

func handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	userID := r.URL.Query().Get("uuid")
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading to websocket: ", err)
		return
	}

	room, ok := getRoom(roomID)
	if !ok {
		// Inform client about the error using the WebSocket connection
		_ = conn.WriteJSON(&types.Message{Content: "Room does not exist", RoomID: roomID, From: "Server", Type: "error", Level: 0, ID: uuid.New().String()})
		_ = conn.Close() // Close the connection since the room does not exist
		return
	}

	// if userID is already in the room, dont add it again
	room.Lock()
	if _, ok := room.Players[userID]; ok {
		room.Unlock()
		// Inform client about the error using the WebSocket connection
		_ = conn.WriteJSON(&types.Message{Content: "User joined the room", RoomID: roomID, From: "Server", Type: "notif", Level: 2, ID: uuid.New().String()})
		return
	}
	room.Unlock()

	room.Lock()
	defer room.Unlock()

	if len(room.Players) >= 2 {
		// Inform client about the error using the WebSocket connection
		_ = conn.WriteJSON(&types.Message{Content: "Room is full", RoomID: roomID, From: "Server", Type: "error", Level: 0, ID: uuid.New().String()})
		_ = conn.Close() // Close the connection since the room is full
		return
	}

	room.Players[userID] = types.Player{Connection: conn, UUID: userID}
	room.LastActivity = time.Now() // Update last activity time

	// Notify the existing user that someone has joined
	for _, player := range room.Players {
		if player.Connection != conn {
			_ = player.Connection.WriteJSON(&types.Message{Content: "User (" + userID + ") has joined the room", RoomID: roomID, From: "Server", Type: "notif", Level: 2, ID: uuid.New().String()})
		}
	}
	go handleRoomMessages(room)
}

func handleRoomMessages(room *types.Room) {
	defer func() {
		room.Lock()
		for _, player := range room.Players {
			_ = player.Connection.Close()
		}
		delete(rooms, room.ID)
		room.Unlock()
	}()

	messageChan := make(chan *types.Message)

	// Routine to handle sending messages to all players
	go func() {
		for msg := range messageChan {
			room.RLock()
			for _, player := range room.Players {
				err := player.Connection.WriteJSON(msg)
				if err != nil {
					fmt.Println("Error writing message to player:", err)
				}
			}
			room.RUnlock()
		}
	}()

	for addr, player := range room.Players {
		go func(addr string, player *websocket.Conn) {
			for {
				msg := &types.Message{}
				err := player.ReadJSON(msg)
				if err != nil {
					fmt.Println("Error reading message from", addr, ":", err)
					room.Lock()
					delete(room.Players, addr) // Remove the disconnected player
					room.Unlock()

					// Inform the remaining user(s) that a user has left
					room.Lock()
					if len(room.Players) == 1 {
						for _, player := range room.Players {
							_ = player.Connection.WriteJSON(&types.Message{Content: "The other user has left the room", RoomID: room.ID, From: "Server", Type: "notif", Level: 2, ID: uuid.New().String()})
						}
					}
					room.Unlock()

					break
				}

				// Broadcast message to all players
				messageChan <- msg
			}
		}(addr, player.Connection)
	}

	for {
		// Update last activity time
		room.Lock()
		room.LastActivity = time.Now()
		room.Unlock()

		time.Sleep(1000 * time.Millisecond) // Avoid busy loop
	}
}

func getRoom(roomID string) (*types.Room, bool) {
	roomsMu.RLock()
	defer roomsMu.RUnlock()

	room, ok := rooms[roomID]
	return room, ok
}

func cleanupRooms() {
	for {
		time.Sleep(5 * time.Minute) // Check every 5 minutes

		roomsMu.Lock()
		for roomID, room := range rooms {
			room.Lock()
			if time.Since(room.LastActivity) > 4*time.Minute && time.Since(room.LastActivity) < 5*time.Minute {
				// Notify the players about the inactivity
				for _, player := range room.Players {
					_ = player.Connection.WriteJSON(&types.Message{Content: "Room will be closed in 1 minute due to inactivity", RoomID: roomID, From: "Server", Type: "notif", Level: 1, ID: uuid.New().String()})
				}
			}
			if len(room.Players) == 0 {
				delete(rooms, roomID)
			}
			// Check if the room is empty and inactive for more than 5 minutes
			if len(room.Players) == 0 && time.Since(room.LastActivity) > 5*time.Minute {
				delete(rooms, roomID)
			}
			room.Unlock()
		}
		roomsMu.Unlock()
	}
}

func handleLeaveRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	userID := r.URL.Query().Get("uuid")

	fmt.Println("User", userID, "leaving room", roomID)

	room, ok := getRoom(roomID)
	if !ok {
		w.Write([]byte("Room does not exist"))
		return
	}

	room.Lock()
	defer room.Unlock()

	if _, ok := room.Players[userID]; !ok {
		w.Write([]byte("User not found in room"))
		return
	}

	delete(room.Players, userID)

	// Inform the remaining user(s) that a user has left
	if len(room.Players) == 1 {
		for _, player := range room.Players {
			_ = player.Connection.WriteJSON(&types.Message{Content: "The other user has left the room", RoomID: room.ID, From: "Server", Type: "notif", Level: 2, ID: uuid.New().String()})
		}
	}
}

var roomsMu sync.RWMutex
