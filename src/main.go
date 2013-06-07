package main

import (
	"./cache"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

const TIMER_INTERVAL = 5 * time.Second
const TTL = 10
const KNOWN_ADDRESS = "128.208.2.88:5002"
const HOST = "128.208.1.139"
const PORT = 20202
const SECRET = "Ian Obermiller -- iano [at] cs.washington.edu -- 47249046"
const LOG_FILE = "log.txt"

var host string
var port int
var secret string
var knownAddress string
var logFile string

var conn2peer = make(map[net.Conn]string)
var peer2conn = make(map[string]net.Conn)
var secrets = make(map[string]bool)

var msgCache = cache.New(1*time.Minute, 30*time.Second)

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	flag.PrintDefaults()
}

func main() {
	flag.StringVar(&host, "host", HOST, "external ip to send in pongs")
	flag.IntVar(&port, "port", PORT, "external port to listen on and send in pongs")
	flag.StringVar(&secret, "secret", SECRET, "secret text to send in replies")
	flag.StringVar(&knownAddress, "seed", KNOWN_ADDRESS, "initial seed node to ping (format host:port)")
	flag.StringVar(&logFile, "log", LOG_FILE, "log file to which secrets are written")

	flag.Usage = Usage
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix(logFile + " ")

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

func tryAddPeer(ipBytes []byte, port uint16) {
	peer := fmt.Sprintf("%v:%v", net.IP(ipBytes).String(), port)
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
	ln, err := net.Listen("tcp", fmt.Sprint(":", port))
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
	send(c, Ping, genId(), []byte{})
}

func pong(c net.Conn, pingMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.Write(net.ParseIP(host).To4())
	send(c, Pong, pingMessageId, buf.Bytes())
}

func query(c net.Conn) {
	send(c, Query, genId(), []byte{})
}

func reply(c net.Conn, queryMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.Write(net.ParseIP(host).To4())
	buf.Write([]byte(secret))
	send(c, Reply, queryMessageId, buf.Bytes())
}

func send(c net.Conn, t MessageType, messageId []byte, payload []byte) {
	b, err := buildMessage(messageId, t, TTL, 0, payload)
	if err != nil {
		return
	}

	//log.Printf("SEND %s %s - % x\n", t.String(), c.RemoteAddr(), b)

	sendRaw(c, b)
}

func sendRaw(c net.Conn, b []byte) {
	key := getKey(b[0:15], MessageType(b[16]))
	msgCache.Set(key, "", 0)

	n, err := c.Write(b)
	if n != len(b) || err != nil {
		log.Println("Err writing to conn: ", err)
		return
	}
}

func handleConnection(c net.Conn) {
	peer := c.RemoteAddr().String()
	conn2peer[c] = peer

	if _, ok := peer2conn[peer]; ok {
		peer2conn[peer] = c
	}

	leftover := []byte{}
	for {	
		b := make([]byte, 2048)
		n, err := c.Read(b)

		if err != nil {
			log.Println("Read err:", err)
			peer = conn2peer[c]
			if _, ok := peer2conn[peer]; ok {
				peer2conn[peer] = nil
			}
			delete(conn2peer, c)
			break
		}

		if n == 0 {
			continue
		}
		
		b = append(leftover, b...)
		n = n + len(leftover)
		leftover = processRead(c, b[:n])
	}
}

func getKey(msgId []byte, mt MessageType) string {
	return fmt.Sprintf("% x - %v", msgId, mt)
}

func processRead(c net.Conn, b []byte) []byte {
	totalLen := len(b)
	processed := 0

	for processed < totalLen {
		newlyProcessed := processMessage(c, b[processed:])
		if newlyProcessed <= 0 {
			return b[processed:]
		}
		processed = processed + newlyProcessed
	}
	return []byte{}
}

// returns the length processed
func processMessage(c net.Conn, b []byte) int {
	peer := c.RemoteAddr().String()
	if len(b) < 23 {
		log.Printf("RECV %s packet too small: % x", peer, b)
		return -1
	}

	buf := bytes.NewBuffer(b[19:])
	var size int32
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		log.Println("Could not read size from Reply message: ", err)
		return -1
	}

	size = size + 22

	if len(b) <= int(size) {
		log.Println("Packet size", len(b), "was less than specified:", size)
		return -1
	}

	b = b[0:size]

	t := MessageType(b[16])
	msgId := b[:16]

	key := getKey(msgId, t)
	if _, found := msgCache.Get(key); found && (t == Ping || t == Query) {
		//log.Println("Ignoring duplicate message", key)
		return int(size)
	}

	//log.Printf("RECV %s %s - % x\n", t, peer, b)

	msgCache.Set(key, peer, 0)

	switch t {
	case Ping:
		go processPing(c, msgId, b)
	case Query:
		go processQuery(c, msgId, b)
	case Pong:
		go processPong(c, msgId, b)
	case Reply:
		go processReply(c, msgId, b)
	}

	return int(size)
}

func processPing(c net.Conn, msgId []byte, b []byte) {
	go pong(c, msgId)
	forwardMessage(c, b, true)
}

func processQuery(c net.Conn, msgId []byte, b []byte) {
	go reply(c, msgId)
	forwardMessage(c, b, true)
}

// Pass true to excludeOriginal to forward to all but the original
// Pass false to forward to only the original
func forwardMessage(originalConn net.Conn, b []byte, excludeOriginal bool) {
	ttl := b[17]

	if ttl <= 1 {
		// don't forward
		return
	}

	b[17] = ttl - 1   // decrement ttl
	b[18] = b[18] + 1 // increment hops

	// forward to all connected peers
	//log.Println("Forwarding to", len(conn2peer)-1, "peers")
	for conn, peer := range conn2peer {
		if (conn == originalConn) == excludeOriginal {
			continue
		}
		peer = peer
		//log.Println("Forwarding message to", peer)
		go sendRaw(conn, b)
	}
}

func processPong(c net.Conn, msgId []byte, b []byte) {
	buf := bytes.NewBuffer(b[23:])

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

	key := getKey(msgId, Ping)
	if peer, found := msgCache.Get(key); found {
		// Send the pong back to the original pinger
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, b, false)
		}
	}
}

func processReply(c net.Conn, msgId []byte, b []byte) {
	buf := bytes.NewBuffer(b[19:])
	var size int32
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		log.Println("Could not read size from Reply message: ", err)
		return
	}

	if size < 6 {
		log.Printf("Invalid reply message, size of payload too short: %v\n% x\n", size, b)
		return
	}

	var port uint16
	err = binary.Read(buf, binary.BigEndian, &port)
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

	size = size - 6
	secretTextBytes := make([]byte, size)
	n, err = buf.Read(secretTextBytes)
	if n != int(size) || err != nil {
		log.Println("n", n, "size", size)
		log.Println("Could not read secret text from Reply message: ", err)
		return
	}

	key := getKey(msgId, Reply)
	if peer, found := msgCache.Get(key); found {
		// Send the reply back to the original pinger
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, b, false)
		}
	}

	msg := fmt.Sprintf("Address %v:%v sent secret text \"%v\"\n", net.IP(ipBytes).String(), port, string(secretTextBytes))

	if _, ok := secrets[msg]; ok {
		return
	}

	secrets[msg] = true

	log.Print(msg)
	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		log.Println("Could not open file: ", err)
	}
	defer file.Close()

	n, err = file.WriteString(msg)
	if n != len(msg) || err != nil {
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

func buildMessage(
	messageId []byte,
	messageType MessageType,
	ttl byte,
	hops byte,
	payload []byte) (b []byte, err error) {

	b = nil
	buf := new(bytes.Buffer)

	n, err := buf.Write(messageId)
	if n != len(messageId) || err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(byte(messageType))
	if err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(ttl)
	if err != nil {
		log.Println("Err:", err)
		return
	}

	err = buf.WriteByte(hops)
	if err != nil {
		log.Println("Err:", err)
		return
	}

	err = binary.Write(buf, binary.BigEndian, int32(len(payload)))
	if err != nil {
		log.Println("Err:", err)
		return
	}

	n, err = buf.Write(payload)
	if n != len(payload) || err != nil {
		log.Println("Err:", err)
		return
	}

	b = buf.Bytes()

	return
}
