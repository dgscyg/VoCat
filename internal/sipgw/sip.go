package sipgw

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type sipMessage struct {
	IsResponse bool
	Method     string
	URI        string
	Status     int
	Reason     string
	Headers    map[string][]string
	Body       []byte
	Raw        string
}

func parseSIP(raw string) (*sipMessage, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	headerEnd := strings.Index(raw, "\n\n")
	if headerEnd < 0 {
		return nil, fmt.Errorf("incomplete SIP")
	}
	head := raw[:headerEnd]
	body := raw[headerEnd+2:]
	lines := strings.Split(head, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty SIP")
	}
	msg := &sipMessage{Headers: map[string][]string{}, Raw: raw}
	start := strings.TrimSpace(lines[0])
	if strings.HasPrefix(start, "SIP/2.0") {
		msg.IsResponse = true
		fields := strings.SplitN(start, " ", 3)
		if len(fields) >= 2 {
			msg.Status, _ = strconv.Atoi(fields[1])
		}
		if len(fields) >= 3 {
			msg.Reason = fields[2]
		}
	} else {
		fields := strings.SplitN(start, " ", 3)
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid SIP start line")
		}
		msg.Method = strings.ToUpper(fields[0])
		msg.URI = fields[1]
	}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(name))
		msg.Headers[key] = append(msg.Headers[key], strings.TrimSpace(value))
	}
	if cl := msg.header("content-length"); cl != "" {
		n, _ := strconv.Atoi(cl)
		if n > 0 && n <= len(body) {
			msg.Body = []byte(body[:n])
		}
	} else if body != "" {
		msg.Body = []byte(body)
	}
	return msg, nil
}

func (msg *sipMessage) header(name string) string {
	values := msg.Headers[strings.ToLower(name)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (msg *sipMessage) callID() string { return strings.TrimSpace(msg.header("call-id")) }

func (msg *sipMessage) cseq() (int, string) {
	fields := strings.Fields(msg.header("cseq"))
	if len(fields) < 2 {
		return 0, ""
	}
	n, _ := strconv.Atoi(fields[0])
	return n, strings.ToUpper(fields[1])
}

func (msg *sipMessage) fromTag() string {
	return headerParam(msg.header("from"), "tag")
}

func (msg *sipMessage) toTag() string {
	return headerParam(msg.header("to"), "tag")
}

func headerParam(value, name string) string {
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		key, val, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), name) {
			return strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	return ""
}

func userFromURI(value string) string {
	start := strings.Index(value, "sip:")
	if start < 0 {
		start = strings.Index(value, "sips:")
		if start < 0 {
			return ""
		}
		start += 5
	} else {
		start += 4
	}
	rest := value[start:]
	if end := strings.IndexAny(rest, "@>; \t"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func md5Hex(parts ...string) string {
	sum := md5.Sum([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(sum[:])
}

func digestResponse(username, realm, password, nonce, uri, method, qop, nc, cnonce string) string {
	ha1 := md5Hex(username, realm, password)
	ha2 := md5Hex(method, uri)
	if qop == "" {
		return md5Hex(ha1, nonce, ha2)
	}
	return md5Hex(ha1, nonce, nc, cnonce, qop, ha2)
}

func parseAuthHeader(value string) map[string]string {
	out := map[string]string{}
	value = strings.TrimSpace(strings.TrimPrefix(value, "Digest"))
	for _, part := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(val), `"`)
	}
	return out
}

func buildSIP(start string, headers [][2]string, body string) string {
	var b strings.Builder
	b.WriteString(start)
	b.WriteString("\r\n")
	if body != "" {
		headers = append(headers, [2]string{"Content-Length", strconv.Itoa(len(body))})
	} else {
		headers = append(headers, [2]string{"Content-Length", "0"})
	}
	for _, header := range headers {
		b.WriteString(header[0])
		b.WriteString(": ")
		b.WriteString(header[1])
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
