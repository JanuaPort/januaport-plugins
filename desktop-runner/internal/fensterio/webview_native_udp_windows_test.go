//go:build windows

package fensterio

// Native Sonden zu Wegen außerhalb des Proxys (SEC-R4 F1): UDP-Senken auf Loopback und auf der eigenen IPv4-Adresse des
// Gasts zählen Pakete und prüfen, ob die Nutzlast einer Seite ankommt. Die Kontrolle läuft ohne das Pflichtargument und
// muss die Pakete sehen; mit dem Argument muss die Zahl null sein.
//
//	TestWindowsWebViewWebRTCNull        beide Senken, STUN und TURN, Produktkonfiguration: 0 Pakete, 0 Nutzlast
//	TestWindowsWebViewWebRTCKontrolle   ohne das Argument kommen Pakete an (sonst ist die Senke oder der Weg blind)

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// udpMarker steht als Benutzername in der Seite; kommt er in einem Paket an, hat die Seite Daten nach außen gebracht.
var udpMarker = fmt.Sprintf("synthetisch-udp-%x", time.Now().UnixNano())

type udpSink struct {
	label   string
	conn    *net.UDPConn
	packets atomic.Int32
	payload atomic.Int32
}

// startUDP startet eine UDP-Senke. Auf STUN-Allocate antwortet sie mit 401, REALM und NONCE; so geht ein Client mit
// Benutzername im zweiten Anlauf weiter und zeigt, ob seine Daten ankommen.
func (b *probeBed) startUDP(label, addr string) *udpSink {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		b.t.Fatalf("%s: %v", label, err)
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		b.t.Fatalf("%s: %v", label, err)
	}
	s := &udpSink{label: label, conn: conn}
	b.t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			s.packets.Add(1)
			if bytes.Contains(buf[:n], []byte(udpMarker)) {
				s.payload.Add(1)
			}
			b.paths.Lock()
			b.hitLog = append(b.hitLog, fmt.Sprintf("+%dms %s UDP %d Bytes", time.Since(b.t0).Milliseconds(), label, n))
			b.paths.Unlock()
			if reply := stun401(buf[:n]); reply != nil {
				_, _ = conn.WriteToUDP(reply, from)
			}
		}
	}()
	return s
}

func (s *udpSink) addr() *net.UDPAddr { return s.conn.LocalAddr().(*net.UDPAddr) }

// stun401 beantwortet eine Allocate-Anfrage (Methode 3) mit dem Fehler 401 samt REALM und NONCE.
func stun401(req []byte) []byte {
	if len(req) < 20 || binary.BigEndian.Uint32(req[4:8]) != 0x2112A442 || binary.BigEndian.Uint16(req[0:2])&0x3EEF != 0x0003 {
		return nil
	}
	attr := func(kind uint16, value []byte) []byte {
		out := make([]byte, 4, 4+len(value)+3)
		binary.BigEndian.PutUint16(out[0:2], kind)
		binary.BigEndian.PutUint16(out[2:4], uint16(len(value)))
		out = append(out, value...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		return out
	}
	var body []byte
	body = append(body, attr(0x0009, append([]byte{0, 0, 4, 1}, "Unauthorized"...))...)
	body = append(body, attr(0x0014, []byte("probe"))...)
	body = append(body, attr(0x0015, []byte("nonce0123456789"))...)
	head := make([]byte, 20)
	binary.BigEndian.PutUint16(head[0:2], 0x0113)
	binary.BigEndian.PutUint16(head[2:4], uint16(len(body)))
	copy(head[4:], req[4:20]) // Cookie und Transaktionsnummer
	return append(head, body...)
}

// rtcFiles baut die Seite, die eine PeerConnection mit dem Server aufbaut und das Sammeln der Adressen anstößt.
func rtcFiles(ice string) map[string]string {
	return map[string]string{
		"index.html": probePage(`<script src="probe.js"></script>`, `<script src="rtc.js"></script>`),
		"probe.js":   probeJS,
		"rtc.js": `
var pc;
try {
  pc = new RTCPeerConnection({iceServers: [` + ice + `]});
  send("rtc_pc", {ok: true});
  pc.createDataChannel("x");
  pc.onicegatheringstatechange = function(){ if (pc.iceGatheringState === "complete") { send("rtc", {fertig: true}); } };
  pc.createOffer().then(function(o){ return pc.setLocalDescription(o); }).catch(function(e){ send("rtc_fehler", {name: e.name}); });
} catch (e) { send("rtc_fehler", {name: e.name}); }
setTimeout(function(){ send("rtc_frist", {}); }, 7000);
`,
	}
}

func rtcICE(kind string, a *net.UDPAddr) string {
	switch kind {
	case "stun":
		return fmt.Sprintf(`{urls: "stun:%s"}`, a)
	default:
		return fmt.Sprintf(`{urls: "turn:%s?transport=udp", username: "%s", credential: "x"}`, a, udpMarker)
	}
}

// runRTC öffnet die Seite und gibt die neuen Pakete der Senke zurück.
func runRTC(t *testing.T, b *probeBed, sink *udpSink, kind string, mutate func(*SurfaceConfig)) (packets, payload int32) {
	t.Helper()
	p0, n0 := sink.packets.Load(), sink.payload.Load()
	s, _, _ := b.openProbe("probe", rtcFiles(rtcICE(kind, sink.addr())), mutate)
	if !pump(t, s, 20*time.Second, func() bool { return b.gw.has("rtc ") || b.gw.has("rtc_frist") || b.gw.has("rtc_fehler") }) {
		t.Fatalf("%s/%s: keine Antwort der Seite", sink.label, kind)
	}
	if !b.gw.has("rtc_pc") && !b.gw.has("rtc_fehler") {
		t.Fatalf("%s/%s: Die Seite hat keine PeerConnection gemeldet (Seite lebt nicht?): %v", sink.label, kind, b.gw.snapshot())
	}
	pump(t, s, 1500*time.Millisecond, nil)
	return sink.packets.Load() - p0, sink.payload.Load() - n0
}

func TestWindowsWebViewWebRTCNull(t *testing.T) {
	b := newProbeBed(t)
	for _, sink := range []*udpSink{b.udp, b.udpExt} {
		for _, kind := range []string{"stun", "turn"} {
			t.Run(sink.label+"_"+kind, func(t *testing.T) {
				b.t = t
				b.run(t, probeFrist, func(t *testing.T) {
					packets, payload := runRTC(t, b, sink, kind, nil)
					t.Logf("%s/%s: %d Pakete, %d mit Nutzlast", sink.label, kind, packets, payload)
					if packets != 0 || payload != 0 {
						t.Fatalf("%s/%s: %d UDP-Pakete (davon %d mit Nutzlast) kamen an der Senke an", sink.label, kind, packets, payload)
					}
				})
			})
		}
	}
}

func TestWindowsWebViewWebRTCKontrolle(t *testing.T) {
	b := newProbeBed(t)
	off := func(c *SurfaceConfig) { c.noWebRTCPolicy = true }
	for _, sink := range []*udpSink{b.udp, b.udpExt} {
		for _, kind := range []string{"stun", "turn"} {
			t.Run(sink.label+"_"+kind, func(t *testing.T) {
				b.t = t
				b.run(t, probeFrist, func(t *testing.T) {
					packets, payload := runRTC(t, b, sink, kind, off)
					t.Logf("%s/%s ohne das Argument: %d Pakete, %d mit Nutzlast", sink.label, kind, packets, payload)
					// Auf der eigenen Adresse des Gasts muss die Kontrolle sehen; auf Loopback ist es nur eine Angabe.
					if sink.label == "extern" && packets == 0 {
						t.Fatalf("Kontrolle blind: Ohne das Argument kam kein UDP-Paket an der Senke %s an", sink.label)
					}
					if sink.label == "extern" && kind == "turn" && payload == 0 {
						t.Fatalf("Kontrolle blind: Die Nutzlast der Seite kam an der Senke %s nicht an", sink.label)
					}
				})
			})
		}
	}
}
