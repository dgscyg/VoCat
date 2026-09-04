package sipgw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"vocat/internal/vowifi"
)

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
	WSSPath  string `json:"wss_path"`
	UDPort   int    `json:"udp_port,omitempty"`
}

type CallController interface {
	Calls(string) ([]vowifi.Call, error)
	DialCall(context.Context, string, string) (vowifi.Call, error)
	AnswerCall(context.Context, string, string) (vowifi.Call, error)
	HangupCall(context.Context, string, string) error
}

type MediaController interface {
	CallMedia(context.Context, string, string) (vowifi.CallMedia, error)
}

type transport interface {
	Send(string) error
}

type endpoint struct {
	deviceID string
	creds    Credentials
	calls    CallController
	media    MediaController

	mu       sync.Mutex
	nonce    string
	ws       transport
	udp      net.PacketConn
	udpAddr  net.Addr
	toTag    string
	pc       *webrtc.PeerConnection
	track    *webrtc.TrackLocalStaticSample
	cancel   context.CancelFunc
	bridged  string
	incoming string
}

func newEndpoint(deviceID string, creds Credentials, calls CallController, media MediaController) *endpoint {
	return &endpoint{deviceID: deviceID, creds: creds, calls: calls, media: media, nonce: randomHex(8)}
}

func (ep *endpoint) handle(raw string, reply func(string), fromUDP net.Addr) {
	msg, err := parseSIP(raw)
	if err != nil || msg == nil {
		return
	}
	if msg.IsResponse {
		ep.handleResponse(msg)
		return
	}
	switch msg.Method {
	case "REGISTER":
		ep.handleRegister(msg, reply)
	case "INVITE":
		ep.handleInvite(msg, reply)
	case "ACK":
	case "BYE", "CANCEL":
		ep.handleHangup(msg, reply)
	case "OPTIONS", "INFO":
		reply(ep.response(msg, 200, "OK", "", nil))
	default:
		reply(ep.response(msg, 501, "Not Implemented", "", nil))
	}
	if fromUDP != nil {
		ep.mu.Lock()
		ep.udpAddr = fromUDP
		ep.mu.Unlock()
	}
}

func (ep *endpoint) handleRegister(msg *sipMessage, reply func(string)) {
	auth := parseAuthHeader(msg.header("authorization"))
	if auth["response"] == "" {
		reply(ep.response(msg, 401, "Unauthorized", "", [][2]string{
			{"WWW-Authenticate", fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5, qop="auth"`, ep.creds.Realm, ep.nonce)},
		}))
		return
	}
	expect := digestResponse(ep.creds.Username, ep.creds.Realm, ep.creds.Password, auth["nonce"], auth["uri"], "REGISTER", auth["qop"], auth["nc"], auth["cnonce"])
	if !strings.EqualFold(auth["username"], ep.creds.Username) || !strings.EqualFold(auth["response"], expect) {
		reply(ep.response(msg, 403, "Forbidden", "", nil))
		return
	}
	expires := msg.header("expires")
	if expires == "" {
		expires = "600"
	}
	contact := msg.header("contact")
	if contact == "" {
		contact = fmt.Sprintf("<sip:%s@local>", ep.creds.Username)
	}
	reply(ep.response(msg, 200, "OK", "", [][2]string{
		{"Contact", contact},
		{"Expires", expires},
	}))
}

func (ep *endpoint) handleInvite(msg *sipMessage, reply func(string)) {
	number := userFromURI(msg.URI)
	if number == "" || number == ep.creds.Username {
		reply(ep.response(msg, 404, "Not Found", "", nil))
		return
	}
	offer := string(msg.Body)
	pc, track, answer, err := answerWebRTC(offer)
	if err != nil {
		reply(ep.response(msg, 488, "Not Acceptable Here", "", nil))
		return
	}
	ep.mu.Lock()
	if ep.pc != nil {
		_ = ep.pc.Close()
	}
	if ep.cancel != nil {
		ep.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ep.pc, ep.track, ep.cancel = pc, track, cancel
	ep.toTag = randomHex(6)
	ep.mu.Unlock()
	reply(ep.response(msg, 100, "Trying", "", nil))
	go func() {
		call, dialErr := ep.calls.DialCall(ctx, ep.deviceID, number)
		if dialErr != nil {
			reply(ep.response(msg, 500, "Server Internal Error", "", nil))
			return
		}
		ep.mu.Lock()
		ep.bridged = call.ID
		ep.mu.Unlock()
		reply(ep.response(msg, 200, "OK", answer, [][2]string{{"Content-Type", "application/sdp"}}))
		go ep.waitAndBridge(ctx, call.ID)
	}()
}

func (ep *endpoint) waitAndBridge(ctx context.Context, callID string) {
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		if ep.media == nil {
			return
		}
		media, err := ep.media.CallMedia(ctx, ep.deviceID, callID)
		if err == nil && media != nil {
			ep.mu.Lock()
			pc, track := ep.pc, ep.track
			ep.mu.Unlock()
			if pc != nil && track != nil {
				bridgeMedia(ctx, pc, track, media)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (ep *endpoint) handleHangup(msg *sipMessage, reply func(string)) {
	ep.mu.Lock()
	callID := ep.bridged
	if ep.cancel != nil {
		ep.cancel()
	}
	if ep.pc != nil {
		_ = ep.pc.Close()
		ep.pc = nil
	}
	ep.bridged = ""
	ep.mu.Unlock()
	if callID != "" && ep.calls != nil {
		_ = ep.calls.HangupCall(context.Background(), ep.deviceID, callID)
	}
	reply(ep.response(msg, 200, "OK", "", nil))
}

func (ep *endpoint) handleResponse(msg *sipMessage) {
	if msg.Status < 200 || msg.Status >= 300 {
		return
	}
	ep.mu.Lock()
	incoming := ep.incoming
	pc := ep.pc
	ep.mu.Unlock()
	if incoming == "" || pc == nil || len(msg.Body) == 0 {
		return
	}
	_ = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(msg.Body)})
	_, _ = ep.calls.AnswerCall(context.Background(), ep.deviceID, incoming)
	go ep.waitAndBridge(context.Background(), incoming)
}

func (ep *endpoint) notifyIncoming(call vowifi.Call, send func(string)) {
	ep.mu.Lock()
	if ep.incoming == call.ID || ep.bridged != "" {
		ep.mu.Unlock()
		return
	}
	pc, track, offer, err := offerWebRTC()
	if err != nil {
		ep.mu.Unlock()
		return
	}
	if ep.pc != nil {
		_ = ep.pc.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ep.pc, ep.track, ep.cancel = pc, track, cancel
	ep.incoming = call.ID
	branch := randomHex(6)
	from := fmt.Sprintf("<sip:%s@%s>;tag=%s", call.Number, ep.creds.Realm, randomHex(6))
	to := fmt.Sprintf("<sip:%s@%s>", ep.creds.Username, ep.creds.Realm)
	ep.mu.Unlock()
	packet := buildSIP("INVITE sip:"+ep.creds.Username+"@"+ep.creds.Realm+" SIP/2.0", [][2]string{
		{"Via", "SIP/2.0/WSS " + ep.creds.Realm + ";branch=z9hG4bK" + branch},
		{"From", from},
		{"To", to},
		{"Call-ID", randomHex(10) + "@" + ep.creds.Realm},
		{"CSeq", "1 INVITE"},
		{"Contact", "<sip:" + ep.creds.Username + "@" + ep.creds.Realm + ";transport=ws>"},
		{"Content-Type", "application/sdp"},
		{"User-Agent", "VoCat"},
	}, offer)
	send(packet)
	go func() {
		<-ctx.Done()
	}()
}

func (ep *endpoint) response(req *sipMessage, status int, reason, body string, extra [][2]string) string {
	to := req.header("to")
	if status >= 180 && headerParam(to, "tag") == "" {
		ep.mu.Lock()
		if ep.toTag == "" {
			ep.toTag = randomHex(6)
		}
		tag := ep.toTag
		ep.mu.Unlock()
		to = to + ";tag=" + tag
	}
	headers := [][2]string{
		{"Via", req.header("via")},
		{"From", req.header("from")},
		{"To", to},
		{"Call-ID", req.header("call-id")},
		{"CSeq", req.header("cseq")},
		{"User-Agent", "VoCat"},
	}
	headers = append(headers, extra...)
	return buildSIP(fmt.Sprintf("SIP/2.0 %d %s", status, reason), headers, body)
}

func newPassword() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
