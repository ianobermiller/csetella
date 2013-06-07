package main

import (
	"./cache"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
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

const TIMER_INTERVAL = 100 * time.Millisecond
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
	send(c, &Message{genId(), Ping, TTL, 0, []byte{}})
}

func pong(c net.Conn, pingMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.Write(net.ParseIP(host).To4())
	send(c, &Message{pingMessageId, Pong, TTL, 0, buf.Bytes()})
}

func query(c net.Conn) {
	send(c, &Message{genId(), Query, TTL, 0, []byte{}})
}

func reply(c net.Conn, queryMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.Write(net.ParseIP(host).To4())
	buf.Write([]byte(secret))

	send(c, &Message{queryMessageId, Reply, TTL, 0, buf.Bytes()})
}

func send(c net.Conn, msg *Message) {
	//log.Println("SEND", msg)

	key := msg.Key()
	msgCache.Set(key, "", 0)

	b, err := msg.Bytes()
	if err != nil {
		log.Println("Err converting msg to bytes: ", err)
		return
	}

	//log.Printf("SEND %s %s - % x\n", t.String(), c.RemoteAddr(), b)
	n, err := c.Write(b)
	if n != len(b) || err != nil {
		log.Println("Err writing to conn: ", err)
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

	if _, ok := peer2conn[peer]; ok {
		peer2conn[peer] = c
	}

	var err error = nil
	for {
		msgId := make([]byte, 16)
		n, err := io.ReadFull(c, msgId)
		if n != 16 || err != nil {
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

		payload := make([]byte, size)
		if size > 0 {
			n, err = io.ReadFull(c, payload)
			if n != int(size) || err != nil {
				log.Println("Err reading payload:", n, err)
				break
			}
		}

		msg := Message{msgId, MessageType(msgType), ttl, hops, payload}
		//log.Println("RECV", msg)
		processMessage(c, &msg)
	}

	log.Println("Read err:", err)
	peer = conn2peer[c]
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
		//log.Println("Ignoring duplicate message", key)
		return
	}

	msgCache.Set(key, peer, 0)

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
	for conn, peer := range conn2peer {
		if (conn == originalConn) == excludeOriginal {
			continue
		}
		peer = peer
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

	textSize := len(msg.Payload) - 6 // 6 bytes for ip and port
	secretTextBytes := make([]byte, textSize)
	n, err = buf.Read(secretTextBytes)
	if n != textSize || err != nil {
		log.Println("Could not read secret text from Reply message: ", err)
		return
	}

	key := getKey(msg.MsgId, Reply)
	if peer, found := msgCache.Get(key); found {
		// Send the reply back to the original pinger
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, msg, false)
		}
	}

	logMsg := fmt.Sprintf("Address %v:%v sent secret text \"%v\"\n", net.IP(ipBytes).String(), port, string(secretTextBytes))

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
