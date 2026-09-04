package sipgw

import (
	"context"
	"hash/fnv"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Manager struct {
	mu      sync.Mutex
	lines   map[string]*endpoint
	calls   CallController
	media   MediaController
	udp     map[int]net.PacketConn
}

func New(calls CallController, media MediaController) *Manager {
	return &Manager{lines: map[string]*endpoint{}, calls: calls, media: media, udp: map[int]net.PacketConn{}}
}

func (manager *Manager) Credentials(deviceID, host string) Credentials {
	deviceID = strings.TrimSpace(deviceID)
	host = strings.TrimSpace(host)
	if host == "" {
		host = "vocat.local"
	}
	if i := strings.Index(host, ":"); i > 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	line := manager.lines[deviceID]
	if line == nil {
		creds := Credentials{
			Username: "webrtc",
			Password: newPassword(),
			Realm:    host,
			WSSPath:  "/api/devices/" + deviceID + "/sip/ws",
			UDPort:   udpPortFor(deviceID),
		}
		line = newEndpoint(deviceID, creds, manager.calls, manager.media)
		manager.lines[deviceID] = line
		go manager.listenUDP(line)
		go manager.watchIncoming(line)
	} else if line.creds.Realm == "vocat.local" && host != "vocat.local" {
		line.creds.Realm = host
	}
	return line.creds
}

func (manager *Manager) AcceptWebSocket(ctx context.Context, conn *websocket.Conn, deviceID, host string) {
	creds := manager.Credentials(deviceID, host)
	manager.mu.Lock()
	line := manager.lines[deviceID]
	manager.mu.Unlock()
	if line == nil {
		return
	}
	send := func(payload string) error {
		return conn.Write(ctx, websocket.MessageText, []byte(payload))
	}
	line.mu.Lock()
	line.ws = websocketSender(send)
	line.creds = creds
	line.mu.Unlock()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		text := strings.TrimSpace(string(data))
		if text == "" || text == "\r\n\r\n" {
			continue
		}
		line.handle(string(data), func(out string) { _ = send(out) }, nil)
	}
}

type websocketSender func(string) error

func (send websocketSender) Send(payload string) error { return send(payload) }

func (manager *Manager) listenUDP(line *endpoint) {
	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(line.creds.UDPort))
	if err != nil {
		return
	}
	manager.mu.Lock()
	manager.udp[line.creds.UDPort] = conn
	manager.mu.Unlock()
	line.mu.Lock()
	line.udp = conn
	line.mu.Unlock()
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		raw := string(buf[:n])
		line.handle(raw, func(out string) { _, _ = conn.WriteTo([]byte(out), addr) }, addr)
	}
}

func (manager *Manager) watchIncoming(line *endpoint) {
	for {
		time.Sleep(500 * time.Millisecond)
		if manager.calls == nil {
			continue
		}
		calls, err := manager.calls.Calls(line.deviceID)
		if err != nil {
			continue
		}
		for _, call := range calls {
			if call.Direction != "incoming" || call.State != "ringing" {
				continue
			}
			line.mu.Lock()
			ws := line.ws
			line.mu.Unlock()
			if ws == nil {
				continue
			}
			line.notifyIncoming(call, func(out string) { _ = ws.Send(out) })
		}
	}
}

func udpPortFor(deviceID string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(deviceID))
	return 15060 + int(hash.Sum32()%900)
}


