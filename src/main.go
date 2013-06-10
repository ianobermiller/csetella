package main

import (
	"./cache"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

type MessageType byte

const (
	Ping  MessageType = 0
	Pong              = 1
	Query             = 2
	Reply             = 3
)

func (t MessageType) String() string {
	switch t {
	case Ping:
		return "PING "
	case Pong:
		return "PONG "
	case Query:
		return "QUERY"
	case Reply:
		return "REPLY"
	default:
		return "NONE "
	}
}

// Debugging
const LOG_PACKETS = false

// Defaults
const (
	TIMER_INTERVAL = 5 * time.Second
	TTL            = 10
	KNOWN_ADDRESS  = "128.208.2.88:5002"
	HOST           = "128.208.1.139"
	PORT           = 20202
	SECRET         = "Ian Obermiller -- iano [at] cs.washington.edu -- 47249046"
	LOG_FILE       = "log.txt"
)

var externalHost string
var externalPort int
var secret string
var knownAddress string
var logFile string

// Maps connections to their peer string (IP:PORT)
var conn2peer = make(map[net.Conn]string)

// Maps a peer string to their connection, which may be nil
var peer2conn = make(map[string]net.Conn)

// Map of secret texts we have seen this sessions, so we don't write them out again
var secrets = make(map[string]bool)

// Cache of recent messages's we have seen, entries expire after 1 minute
// Key is msgId-type.
// Value is the peer string, so this is double duty as the routing table for incoming
//  pongs and replies.
var msgCache = cache.New(1*time.Minute, 30*time.Second)

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	flag.PrintDefaults()
}

func main() {
	flag.StringVar(&externalHost, "host", HOST, "external ip to send in pongs")
	flag.IntVar(&externalPort, "port", PORT, "external port to listen on and send in pongs")
	flag.StringVar(&secret, "secret", SECRET, "secret text to send in replies")
	flag.StringVar(&knownAddress, "seed", KNOWN_ADDRESS, "initial seed node to ping (format host:port)")
	flag.StringVar(&logFile, "log", LOG_FILE, "log file to which secrets are written")

	flag.Usage = Usage
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix(logFile + " ")

	go startWebServer()
	go startListener()

	tryAddPeerString(knownAddress)

	shouldPing := true
	for {
		for p, c := range peer2conn {
			if c == nil {
				go connect(p)
			}
		}

		for c, _ := range conn2peer {
			if shouldPing {
				go ping(c)
			} else {
				go query(c)
			}
		}

		if len(conn2peer) > 0 {
			shouldPing = !shouldPing
		}
		time.Sleep(TIMER_INTERVAL)
	}
}

type templateParams struct {
	Replies map[string]bool
	Uptime  string
	Peers   map[net.Conn]string
}

var startTime = time.Now()

func startWebServer() {
	t, err := template.New("mainTemplate").Parse(`
<html><body>
<h1>Iano's CSEtella Node</h1>

<h2>Stats</h2>
Uptime: {{.Uptime}}

<h2>Peers</h2>
<ul>
	{{range $_, $peer := .Peers}}
	<li>{{$peer}}</li>
	{{end}}
</ul>

<h2>Replies Observed</h2>
<ul>
	{{range $reply, $_ := .Replies}}
	<li>{{$reply}}</li>
	{{end}}
</ul>

</body></html>`)
	if err != nil {
		log.Fatalln("Error compiling template: ", err)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := templateParams{
			Replies: secrets,
			Uptime:  time.Now().Sub(startTime).String(),
			Peers:   conn2peer,
		}

		err = t.Execute(w, p)
	})

	log.Fatal(http.ListenAndServe(":20201", nil))
}

func tryAddPeer(ipBytes []byte, port uint16) {
	ip := net.IP(ipBytes).String()

	if ip == externalHost && int(port) == externalPort {
		// don't connect to self
		return
	}

	peer := fmt.Sprintf("%v:%v", ip, port)
	tryAddPeerString(peer)
}

func tryAddPeerString(peer string) {
	for p, _ := range peer2conn {
		if p == peer {
			return
		}
	}
	log.Println("Adding new peer", peer)
	peer2conn[peer] = nil
}

func connect(address string) {
	log.Println("Connecting to", address)
	conn, err := net.Dial("tcp", address)
	if err != nil {
		log.Println("Dial err:", err)
		return
	}

	go handleConnection(conn)
}

func startListener() {
	ln, err := net.Listen("tcp", fmt.Sprint(":", externalPort))
	if err != nil {
		log.Println("Listen error: ", err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept error: ", err)
			continue
		}

		peer := conn.RemoteAddr().String()
		log.Printf("Address %v connected to us!\n", peer)
		conn2peer[conn] = peer

		go handleConnection(conn)
	}
}

func ping(c net.Conn) {
	send(c, &Message{genId(), Ping, TTL, 0, []byte{}})
}

func pong(c net.Conn, pingMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(externalPort))
	buf.Write(net.ParseIP(externalHost).To4())
	send(c, &Message{pingMessageId, Pong, TTL, 0, buf.Bytes()})
}

func query(c net.Conn) {
	send(c, &Message{genId(), Query, TTL, 0, []byte{}})
}

func reply(c net.Conn, queryMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(externalPort))
	buf.Write(net.ParseIP(externalHost).To4())
	buf.Write([]byte(secret))

	send(c, &Message{queryMessageId, Reply, TTL, 0, buf.Bytes()})
}

func send(c net.Conn, msg *Message) {
	if LOG_PACKETS {
		log.Println("SEND", msg)
	}

	if _, ok := conn2peer[c]; !ok {
		return
	}

	b, err := msg.Bytes()
	if err != nil {
		log.Println("Err converting msg to bytes: ", err)
		return
	}

	n, err := c.Write(b)
	if n != len(b) || err != nil {
		log.Println("Err writing to conn: ", err)
		killConnection(c)
		return
	}
}

func readByte(r io.Reader) (b byte, err error) {
	bs := make([]byte, 1)
	n, err := r.Read(bs)
	if err != nil {
		return
	}
	if n != 1 {
		err = errors.New("Couldn't even read one byte.")
		return
	}
	return bs[0], nil
}

type Message struct {
	MsgId   []byte
	MsgType MessageType
	TTL     byte
	Hops    byte
	Payload []byte
}

func handleConnection(c net.Conn) {
	peer := c.RemoteAddr().String()
	conn2peer[c] = peer

	fmt.Println("Connected to ", peer)

	if _, ok := peer2conn[peer]; ok {
		peer2conn[peer] = c
	}

	var err error = nil
	for {
		msgId := make([]byte, 16)
		n, err := io.ReadFull(c, msgId)
		if n != 16 {
			err = errors.New(fmt.Sprint("Could not read 16 bytes for msgid, only got", n))
			break
		}
		if err != nil {
			break
		}

		msgType, err := readByte(c)
		if err != nil {
			break
		}

		ttl, err := readByte(c)
		if err != nil {
			break
		}

		hops, err := readByte(c)
		if err != nil {
			break
		}

		var size int32
		err = binary.Read(c, binary.BigEndian, &size)
		if err != nil {
			break
		}

		if size > 2048 {
			err = errors.New(fmt.Sprint("Payload size", size, "was unexpectedly large."))
			break
		}

		payload := make([]byte, size)
		if size > 0 {
			n, err = io.ReadFull(c, payload)
			if n != int(size) || err != nil {
				log.Println("Err reading payload:", n, err)
				break
			}
		}

		msg := Message{msgId, MessageType(msgType), ttl, hops, payload}
		processMessage(c, &msg)
	}

	log.Println("Read err:", err)
	killConnection(c)
}

func killConnection(c net.Conn) {
	log.Println("Closing conn:", c.RemoteAddr())
	peer := conn2peer[c]
	if _, ok := peer2conn[peer]; ok {
		peer2conn[peer] = nil
	}
	delete(conn2peer, c)
}

func (msg *Message) Key() string {
	return getKey(msg.MsgId, msg.MsgType)
}

func getKey(msgId []byte, mt MessageType) string {
	return fmt.Sprintf("% x - %v", msgId, mt)
}

// returns the length processed
func processMessage(c net.Conn, msg *Message) {
	peer := c.RemoteAddr().String()

	key := msg.Key()
	if _, found := msgCache.Get(key); found && (msg.MsgType == Ping || msg.MsgType == Query) {
		//log.Println("Ignoring duplicate message", msg)
		return
	}

	msgCache.Set(key, peer, 0)

	if LOG_PACKETS {
		log.Println("RECV", msg)
	}

	switch msg.MsgType {
	case Ping:
		go processPing(c, msg)
	case Query:
		go processQuery(c, msg)
	case Pong:
		go processPong(c, msg)
	case Reply:
		go processReply(c, msg)
	}
}

func processPing(c net.Conn, msg *Message) {
	go pong(c, msg.MsgId)
	forwardMessage(c, msg, true)
}

func processQuery(c net.Conn, msg *Message) {
	go reply(c, msg.MsgId)
	forwardMessage(c, msg, true)
}

// Pass true to excludeOriginal to forward to all but the original
// Pass false to forward to only the original
func forwardMessage(originalConn net.Conn, msg *Message, excludeOriginal bool) {
	if msg.TTL <= 1 {
		// don't forward
		return
	}

	msg.TTL = msg.TTL - 1   // decrement ttl
	msg.Hops = msg.Hops + 1 // increment hops

	// forward to all connected peers
	for conn, _ := range conn2peer {
		if (conn == originalConn) == excludeOriginal {
			continue
		}
		go send(conn, msg)
	}
}

func processPong(c net.Conn, msg *Message) {
	buf := bytes.NewBuffer(msg.Payload)

	var port uint16
	err := binary.Read(buf, binary.BigEndian, &port)
	if err != nil {
		log.Println("Could not read port from Pong message: ", err)
		return
	}

	ipBytes := make([]byte, 4)
	n, err := buf.Read(ipBytes)
	if n != 4 || err != nil {
		log.Println("Could not read ip from Pong message: ", err)
		return
	}

	tryAddPeer(ipBytes, port)

	key := getKey(msg.MsgId, Ping)
	if peer, found := msgCache.Get(key); found {
		// Send the pong back to the original pinger
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, msg, false)
		}
	}
}

func processReply(c net.Conn, msg *Message) {
	buf := bytes.NewBuffer(msg.Payload)

	var port uint16
	err := binary.Read(buf, binary.BigEndian, &port)
	if err != nil {
		log.Println("Could not read port from Pong message: ", err)
		return
	}

	ipBytes := make([]byte, 4)
	n, err := buf.Read(ipBytes)
	if n != 4 || err != nil {
		log.Println("Could not read ip from Pong message: ", err)
		return
	}

	tryAddPeer(ipBytes, port)

	secretText := string(msg.Payload[6:])

	key := getKey(msg.MsgId, Reply)
	if peer, found := msgCache.Get(key); found {
		// Send the reply back to the original pinger
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, msg, false)
		}
	}

	logMsg := fmt.Sprintf("Address %v:%v sent secret text \"%v\" (size %v)\n", net.IP(ipBytes).String(), port, secretText, len(msg.Payload)-6)

	if _, ok := secrets[logMsg]; ok {
		return
	}

	secrets[logMsg] = true

	log.Print(logMsg)
	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		log.Println("Could not open file: ", err)
	}
	defer file.Close()

	n, err = file.WriteString(logMsg)
	if n != len(logMsg) || err != nil {
		log.Println("WriteString error to log: ", err)
	}
	file.Sync()
}

func genId() (messageId []byte) {
	messageId = make([]byte, 16)
	_, err := io.ReadFull(rand.Reader, messageId)
	if err != nil {
		fmt.Errorf("ReadFull from rand err: %s\n", err)
	}
	return
}

func (msg *Message) Bytes() (b []byte, err error) {

	b = nil
	buf := new(bytes.Buffer)

	n, err := buf.Write(msg.MsgId)
	if n != len(msg.MsgId) || err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(byte(msg.MsgType))
	if err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(msg.TTL)
	if err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(msg.Hops)
	if err != nil {
		log.Println("Err:", err)
		return
	}

	size := int32(len(msg.Payload))

	err = binary.Write(buf, binary.BigEndian, size)
	if err != nil {
		log.Println("Err:", err)
		return
	}

	n, err = buf.Write(msg.Payload)
	if n != len(msg.Payload) || err != nil {
		log.Println("Err:", err)
		return
	}

	b = buf.Bytes()

	return
}
