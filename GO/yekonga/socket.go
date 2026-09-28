package yekonga

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"

	// "github.com/robertkonga/yekonga-server-go/plugins/socketio"
	// "github.com/robertkonga/yekonga-server-go/plugins/socketio/engineio"
	// "github.com/robertkonga/yekonga-server-go/plugins/socketio/engineio/transport"
	// "github.com/robertkonga/yekonga-server-go/plugins/socketio/engineio/transport/polling"
	// "github.com/robertkonga/yekonga-server-go/plugins/socketio/engineio/transport/websocket"

	"github.com/robertkonga/yekonga-server-go/plugins/websocket"
)

// Easier to get running with CORS. Thanks for help @Vindexus and @erkie
var allowOriginFunc = func(r *http.Request) bool {
	return true
}

func NewSocketServer(y *YekongaData) *SocketServer {
	s := &SocketServer{
		Namespaces: make(map[string]*Namespace),
		App:        y,
	}

	root := s.Of("/") // Root namespace "/"

	root.OnConnect(func(c *Client) {
		console.Log("New client connected: %s (namespace: /)", c.ID)
		c.EmitToClient("message", "Welcome to the YekongaGo WebSocket server!")

		// c.On("chat_message", func(data interface{}) {
		// 	console.Log("Received === chat_message from %s: %s", c.id, data)
		// })

		// c.EmitToClient("message", "Welcome to the Go WebSocket server!")
		// c.EmitToClient("message", datatype.DataMap{"message": "robert"})
	})

	root.On("subscribe", func(c *Client, content interface{}) {
		console.Log("Subscribe clientID %s: %s", c.ID, content)

		data := helper.ToMap[interface{}](content)
		userId := helper.GetMapString(data, "userId")
		deviceId := helper.GetMapString(data, "deviceId")

		root.JoinRoom(userId, c)
		root.JoinRoom(deviceId, c)
		// Yekonga.Cloud.runOnMessage(null, "subscribe", data);
	})

	root.On("unsubscribe", func(c *Client, deviceId interface{}) {
		console.Log("unsubscribe device", deviceId)

		if id, ok := deviceId.(string); ok {
			root.LeaveRoom(id, c)
		}
		// Yekonga.Cloud.runOnMessage(null, "unsubscribe", deviceId);
	})

	root.On("acknowledge", func(c *Client, id interface{}) {
		console.Log("acknowledge clientID %s: %s", c.ID, id)

		if v, ok := id.(string); ok {
			root.App.ModelQuery("PushNotification").Update(datatype.DataMap{
				"acknowledged": true,
				"status":       "delivered",
			}, datatype.DataMap{
				"id": v,
			})
		}
		// Yekonga.Cloud.runOnMessage(null, "acknowledge", id);
	})

	root.On("graphql-request", func(c *Client, content interface{}) {
		data := helper.ToMap[interface{}](content)
		query := helper.GetMapString(data, "body.query")
		variables := helper.GetMap(data, "body.variables")
		listener := helper.GetMapString(data, "listener")

		response := root.App.GraphQL(query, variables, c.Request, c.Response)

		root.EmitToClient(c, "graphql-response", datatype.DataMap{
			"listener": listener,
			"body":     response,
		})
		// Yekonga.Cloud.runOnMessage(null, "graphql-request", data);
	})

	root.On("run-on-server", func(c *Client, content interface{}) {
		console.Log("data", content)
		// Yekonga.Cloud.runOnMessage(null, "run-on-server", content);
	})

	root.On("run-on-client", func(c *Client, content interface{}) {
		console.Log("data", content)
		// Yekonga.Cloud.runOnMessage(null, "run-on-client", content);
	})

	root.On("run-on-desktop", func(c *Client, content interface{}) {
		console.Log("data", content)

		// Yekonga.Cloud.runOnMessage(null, "run-on-desktop", content);
	})

	root.OnError(func(c *Client, err error) {
		console.Log("Error on client %s: %v", c.ID, err)
	})

	root.OnDisconnect(func(c *Client, reason string) {
		console.Log("Client disconnected: %s (reason: %s)", c.ID, reason)
	})

	return s
}

// Namespace represents an isolated group (like Socket.IO namespace)
type Namespace struct {
	mu      sync.Mutex
	Clients map[string]*Client
	Rooms   map[string]map[string]bool
	App     *YekongaData

	// Socket.IO-style event handlers for this namespace
	onConnectCallback    func(c *Client)
	onDisconnectCallback func(c *Client, reason string)
	onErrorCallback      func(c *Client, err error)

	// Custom event handlers: event name -> handler function
	eventHandlers map[string]func(c *Client, data interface{})
}

// Client represents a connected WebSocket client
type Client struct {
	conn          *websocket.Conn
	send          chan []byte
	eventHandlers map[string]func(data interface{})

	ID        string
	Namespace *Namespace
	Rooms     map[string]bool // rooms this client is in (for quick lookup)
	Request   *Request
	Response  *Response
}

// Message incoming from client
type Message struct {
	Type  string          `json:"type"`
	MsgID *string         `json:"msgId,omitempty"`
	Data  json.RawMessage `json:"data"`
}

// EventMessage outgoing event
type EventMessage struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// AckMessage acknowledgment response
type AckMessage struct {
	Type    string      `json:"type"`
	MsgID   string      `json:"msgId"`
	Payload interface{} `json:"payload,omitempty"`
}

// SocketServer manages multiple namespaces
type SocketServer struct {
	mu         sync.Mutex
	Namespaces map[string]*Namespace
	App        *YekongaData
}

// of returns or creates a namespace
func (s *SocketServer) Of(path string) *Namespace {
	if s == nil {
		return &Namespace{}
	}
	return s.getOrCreateNamespace(path)
}

// of returns or creates a namespace
func (s *SocketServer) Close() {

}

// getOrCreateNamespace returns or creates a namespace
func (s *SocketServer) getOrCreateNamespace(path string) *Namespace {
	if s == nil {
		return &Namespace{}
	}

	path = strings.Trim(path, " ")
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if n, ok := s.Namespaces[path]; ok {
		return n
	}

	n := &Namespace{
		App:           s.App,
		Clients:       make(map[string]*Client),
		Rooms:         make(map[string]map[string]bool),
		eventHandlers: make(map[string]func(c *Client, data interface{})),
	}
	s.Namespaces[path] = n
	return n
}

// WebSocket handler
func (s *SocketServer) ServeWS(req *Request, res *Response) {
	w := res.httpResponseWriter
	r := req.HttpRequest

	nsPath := r.URL.Query().Get("ns")
	if nsPath == "" {
		nsPath = "/"
	}

	namespace := s.getOrCreateNamespace(nsPath)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(*w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}

	clientID := helper.UUID()
	client := &Client{
		conn:          conn,
		send:          make(chan []byte, 256),
		eventHandlers: make(map[string]func(data interface{})),

		ID:        clientID,
		Namespace: namespace,
		Rooms:     make(map[string]bool),
		Request:   req,
		Response:  res,
	}

	namespace.addClient(client)

	// Send client ID (compatible with previous client code)
	idMsg, _ := json.Marshal(EventMessage{Event: "id", Data: json.RawMessage(`"` + clientID + `"`)})
	client.send <- idMsg

	go client.writePump()
	client.readPump()
}

// Socket.IO-style registration methods (applied to a namespace)

func (n *Namespace) OnConnect(handler func(c *Client)) {
	n.onConnectCallback = handler
}

func (n *Namespace) OnDisconnect(handler func(c *Client, reason string)) {
	n.onDisconnectCallback = handler
}

func (n *Namespace) OnError(handler func(c *Client, err error)) {
	n.onErrorCallback = handler
}

func (n *Namespace) On(event string, handler func(c *Client, data interface{})) {
	n.eventHandlers[event] = handler
}

func (n *Namespace) OnEvent(event string, handler func(c *Client, data interface{})) {
	n.eventHandlers[event] = handler
}

// Internal methods (same as before, slightly refactored)

func (n *Namespace) addClient(c *Client) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Clients[c.ID] = c
	if n.onConnectCallback != nil {
		n.onConnectCallback(c)
	}
}

func (n *Namespace) removeClient(c *Client, reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.Clients, c.ID)

	// Remove from all rooms
	for room := range c.Rooms {
		if roomClients, ok := n.Rooms[room]; ok {
			delete(roomClients, c.ID)
			if len(roomClients) == 0 {
				delete(n.Rooms, room)
			}
		}
	}
	c.Rooms = nil

	if n.onDisconnectCallback != nil {
		n.onDisconnectCallback(c, reason)
	}
}

func (n *Namespace) JoinRoom(room string, c *Client) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, ok := n.Rooms[room]; !ok {
		n.Rooms[room] = make(map[string]bool)
	}
	n.Rooms[room][c.ID] = true
	c.Rooms[room] = true
}

func (n *Namespace) LeaveRoom(room string, c *Client) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if roomClients, ok := n.Rooms[room]; ok {
		delete(roomClients, c.ID)
		if len(roomClients) == 0 {
			delete(n.Rooms, room)
		}
	}
	delete(c.Rooms, room)
}

func (n *Namespace) EmitToClient(c *Client, event string, data interface{}) {
	if n == nil || c == nil {
		return
	}

	msg, err := encodeEvent(event, data)
	if err != nil {
		return
	}

	c.sendMessage(msg)
}

// encodeEvent builds the wire message for an event. Messages going to many
// clients are encoded once and the same bytes sent to each.
func encodeEvent(event string, data interface{}) ([]byte, error) {
	return json.Marshal(EventMessage{Event: event, Data: helper.ToByte(data)})
}

// sendMessage queues an encoded message, dropping it if the client's buffer
// is full rather than blocking the sender.
func (c *Client) sendMessage(msg []byte) {
	select {
	case c.send <- msg:
	default:
	}
}

func (n *Namespace) SendToRoom(room, event string, data interface{}, exclude *Client) {
	if n == nil {
		return
	}

	n.mu.Lock()
	clientsInRoom := make([]*Client, 0)
	if roomClients, ok := n.Rooms[room]; ok {
		for cid := range roomClients {
			if client, exists := n.Clients[cid]; exists && client != exclude {
				clientsInRoom = append(clientsInRoom, client)
			}
		}
	}
	n.mu.Unlock()

	n.sendToClients(clientsInRoom, event, data)
}

func (n *Namespace) Emit(event string, data interface{}, exclude *Client) {
	if n == nil {
		return
	}
	n.Broadcast(event, data, exclude)
}

func (n *Namespace) Broadcast(event string, data interface{}, exclude *Client) {
	n.broadcastWhere(event, data, func(client *Client) bool { return client != exclude })
}

// broadcastToTenant sends an event to the clients of tenantId and to clients
// without a tenant (e.g. main-domain admin screens). An empty tenantId sends
// to everyone.
func (n *Namespace) broadcastToTenant(tenantId string, event string, data interface{}) {
	if tenantId == "" {
		n.broadcastWhere(event, data, func(*Client) bool { return true })
		return
	}

	n.broadcastWhere(event, data, func(client *Client) bool {
		clientTenant := client.tenantId()
		return clientTenant == "" || clientTenant == tenantId
	})
}

func (n *Namespace) broadcastWhere(event string, data interface{}, include func(*Client) bool) {
	if n == nil {
		return
	}

	n.mu.Lock()
	clients := make([]*Client, 0, len(n.Clients))
	for _, client := range n.Clients {
		if include(client) {
			clients = append(clients, client)
		}
	}
	n.mu.Unlock()

	n.sendToClients(clients, event, data)
}

func (n *Namespace) sendToClients(clients []*Client, event string, data interface{}) {
	if len(clients) == 0 {
		return
	}

	msg, err := encodeEvent(event, data)
	if err != nil {
		return
	}

	for _, client := range clients {
		client.sendMessage(msg)
	}
}

// tenantId is the tenant the client connected as, resolved from its
// connection request like any other request's; "" when it has none.
func (c *Client) tenantId() string {
	if c == nil || c.Request == nil {
		return ""
	}

	return idString(c.Request.TenantId())
}

func (c *Client) On(event string, handler func(data interface{})) {
	if c == nil {
		return
	}
	c.eventHandlers[event] = handler
}

func (c *Client) OnEvent(event string, handler func(data interface{})) {
	if c == nil {
		return
	}
	c.eventHandlers[event] = handler
}

func (c *Client) Emit(event string, data interface{}) {
	if c == nil || c.Namespace == nil {
		return
	}
	c.Namespace.Emit(event, data, c)
}

func (c *Client) Broadcast(event string, data interface{}) {
	if c == nil || c.Namespace == nil {
		return
	}
	c.Namespace.Broadcast(event, data, c)
}

func (c *Client) SendToRoom(room string, event string, data interface{}) {
	if c == nil || c.Namespace == nil {
		return
	}
	c.Namespace.SendToRoom(room, event, data, c)
}

func (c *Client) EmitToClient(event string, data interface{}) {
	if c == nil || c.Namespace == nil {
		return
	}
	c.Namespace.EmitToClient(c, event, data)
}

func (c *Client) SendBack(event string, data interface{}) {
	if c == nil || c.Namespace == nil {
		return
	}
	c.Namespace.EmitToClient(c, event, data)
}

// readPump + message handling
func (c *Client) readPump() {
	defer func() {
		c.Namespace.removeClient(c, "closed")
		c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Read error: %v", err)
			}
			reason := "error"
			if websocket.IsCloseError(err) {
				reason = "closed"
			}
			c.Namespace.removeClient(c, reason)
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		c.handleMessage(&msg)
	}
}

func (c *Client) handleMessage(msg *Message) {
	n := c.Namespace
	var ackPayload interface{} = "ok"
	var err error

	// First, check if it's a custom event registered via OnEvent
	if msg.Type == "emit" {
		var d struct {
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data, &d) == nil && d.Event != "" {
			payload, _ := helper.ToInterface(d.Payload)

			if handler, ok := c.eventHandlers[d.Event]; ok {
				handler(payload)

				// Custom handlers are responsible for replying if needed
				if msg.MsgID != nil {
					ackMsg, _ := json.Marshal(AckMessage{Type: "ack", MsgID: *msg.MsgID, Payload: "ok"})
					c.send <- ackMsg
				}
				return
			}

			if handler, ok := n.eventHandlers[d.Event]; ok {
				handler(c, payload)

				// Custom handlers are responsible for replying if needed
				if msg.MsgID != nil {
					ackMsg, _ := json.Marshal(AckMessage{Type: "ack", MsgID: *msg.MsgID, Payload: "ok"})
					c.send <- ackMsg
				}
				return
			}
		}
	}

	// Built-in protocol handling
	switch msg.Type {
	case "join":
		var d struct {
			Room string `json:"room"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			n.JoinRoom(d.Room, c)
		}
	case "leave":
		var d struct {
			Room string `json:"room"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			n.LeaveRoom(d.Room, c)
		}
	case "rejoinRooms":
		var d struct {
			Rooms []string `json:"rooms"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			for _, room := range d.Rooms {
				n.JoinRoom(room, c)
			}
		}
	case "createChannel":
		// No-op, rooms created on demand
	case "emit", "broadcast":
		var d struct {
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			n.Broadcast(d.Event, d.Payload, c)
		}
	case "toRoom":
		var d struct {
			Room    string          `json:"room"`
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			n.SendToRoom(d.Room, d.Event, d.Payload, c)
		}
	case "toClient":
		var d struct {
			ClientID string          `json:"clientId"`
			Event    string          `json:"event"`
			Payload  json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(msg.Data, &d) == nil {
			n.mu.Lock()
			target, ok := n.Clients[d.ClientID]
			n.mu.Unlock()
			if ok {
				n.EmitToClient(target, d.Event, d.Payload)
			}
		}
	}

	if err != nil {
		ackPayload = err.Error()
	}

	if msg.MsgID != nil {
		ackMsg, _ := json.Marshal(AckMessage{
			Type:    "ack",
			MsgID:   *msg.MsgID,
			Payload: ackPayload,
		})
		c.send <- ackMsg
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// func main() {
// 	var addr = flag.String("addr", ":8080", "http service address")
// 	flag.Parse()

// 	server := NewSocketServer()

// 	http.HandleFunc("/ws", server.serveWS)

// 	log.Printf("WebSocket server starting on %s", *addr)
// 	log.Printf("Connect with: ws://localhost%s/ws?ns=/         (root namespace)", *addr)
// 	log.Printf("           or: ws://localhost%s/ws?ns=/chat     (custom namespace)", *addr)
// 	log.Fatal(http.ListenAndServe(*addr, nil))
// }
