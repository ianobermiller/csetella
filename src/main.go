package main

import (
	"./cache"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
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

const TIMER_INTERVAL = 200 * time.Second
const KNOWN_ADDRESS = "128.208.2.88:5002"
const HOST = "128.208.1.137"
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
var gotSecret = make(map[string]bool)
var msgCache = cache.New(5*time.Minute, 30*time.Second)

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	flag.PrintDefaults()
}

func main() {
	flag.StringVar(&host, "host", HOST, "external ip to send in pongs")
	flag.IntVar(&port, "port", PORT, "external port to send in pongs")
	flag.StringVar(&secret, "secret", SECRET, "secret text to send in replies")
	flag.StringVar(&knownAddress, "seed", KNOWN_ADDRESS, "initial seed node to ping (format host:port)")
	flag.StringVar(&logFile, "log", LOG_FILE, "log file to which secrets are written")

	flag.Usage = Usage
	flag.Parse()

	go startListener()

	go connect(knownAddress)

	shouldPing := true
	for {
		for c, _ := range conn2peer {
			if shouldPing {
				go ping(c)
			} else {
				go query(c)
			}
		}

		for p, c := range peer2conn {
			if c == nil {
				go connect(p)
			}
		}

		shouldPing = !shouldPing
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
	fmt.Println("Adding new peer ", peer)
	peer2conn[peer] = nil
}

func connect(address string) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Println("Dial err:", err)
		return
	}

	go handleConnection(conn)
}

func startListener() {
	ln, err := net.Listen("tcp", fmt.Sprint(":", port))
	if err != nil {
		fmt.Println("Listen error: ", err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Accept error: ", err)
			continue
		}

		peer := conn.RemoteAddr().String()
		fmt.Printf("Address %v connected to us!\n", peer)

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
	send(c, Reply, queryMessageId, []byte(secret))
}

func send(c net.Conn, t MessageType, messageId []byte, payload []byte) {
	b, err := buildMessage(messageId, t, 10, 0, payload)
	if err != nil {
		return
	}

	fmt.Printf("SEND %s %s - % x\n", t.String(), c.RemoteAddr(), b)

	sendRaw(c, b)
}

func sendRaw(c net.Conn, b []byte) {
	n, err := c.Write(b)
	if n != len(b) || err != nil {
		fmt.Println("Err writing to conn: ", err)
		return
	}
}

func handleConnection(c net.Conn) {
	peer := c.RemoteAddr().String()
	conn2peer[c] = peer
	// `peer` is not the external address, so don't add to peer2conn

	b := make([]byte, 2048)
	for {
		n, err := c.Read(b)

		if err != nil {
			fmt.Println("Read err:", err)
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

		processMessage(c, b[:n])
	}
}

func getKey(msgId []byte, mt MessageType) string {
	return fmt.Sprint(msgId, "-", mt)
}

func processMessage(c net.Conn, b []byte) {
	peer := c.RemoteAddr().String()
	if len(b) < 23 {
		fmt.Printf("RECV %s packet too small: % x", peer, b)
		return
	}

	t := MessageType(b[16])
	msgId := b[:16]
	fmt.Printf("RECV %s %s - % x\n", t, peer, b)

	key := getKey(msgId, t)
	if _, found := msgCache.Get(key); found {
		return
	}

	msgCache.Set(key, true, 0)

	switch t {
	case Ping:
		go pong(c, msgId)
		forwardMessage(c, b)
	case Query:
		go reply(c, msgId)
		forwardMessage(c, b)
	case Pong:
		go processPong(c, b)
	case Reply:
		go processReply(c, b)
	}
}

func forwardMessage(originalConn net.Conn, b []byte) {
	ttl := b[17]

	if ttl <= 1 {
		// don't forward
		return
	}

	b[17] = ttl - 1   // decrement ttl
	b[18] = b[18] + 1 // increment hops

	// forward to all connected peers
	for conn, _ := range conn2peer {
		if conn != originalConn {
			continue
		}
		go sendRaw(conn, b)
	}
}

func processPong(c net.Conn, b []byte) {
	buf := bytes.NewBuffer(b[23:])

	var port uint16
	err := binary.Read(buf, binary.BigEndian, &port)
	if err != nil {
		fmt.Println("Could not read port from Pong message: ", err)
		return
	}

	ipBytes := make([]byte, 4)
	n, err := buf.Read(ipBytes)
	if n != 4 || err != nil {
		fmt.Println("Could not read ip from Pong message: ", err)
		return
	}

	tryAddPeer(ipBytes, port)
}

func processReply(c net.Conn, b []byte) {
	buf := bytes.NewBuffer(b[19:])
	var size int32
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		fmt.Println("Could not read size from Reply message: ", err)
		return
	}

	var port uint16
	err = binary.Read(buf, binary.BigEndian, &port)
	if err != nil {
		fmt.Println("Could not read port from Pong message: ", err)
		return
	}

	ipBytes := make([]byte, 4)
	n, err := buf.Read(ipBytes)
	if n != 4 || err != nil {
		fmt.Println("Could not read ip from Pong message: ", err)
		return
	}

	tryAddPeer(ipBytes, port)

	size = size - 6
	secretTextBytes := make([]byte, size)
	n, err = buf.Read(secretTextBytes)
	if n != int(size) || err != nil {
		fmt.Println("n", n, "size", size)
		fmt.Println("Could not read secret text from Reply message: ", err)
		return
	}

	log := fmt.Sprintf("Address %v:%v sent secret text \"%v\"\n", net.IP(ipBytes).String(), port, string(secretTextBytes))
	fmt.Print(log)
	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		fmt.Println("Could not open file: ", err)
	}
	defer file.Close()

	n, err = file.WriteString(log)
	if n != len(log) || err != nil {
		fmt.Println("WriteString error to log: ", err)
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
		fmt.Println("Err:", err)
		return
	}

	err = buf.WriteByte(byte(messageType))
	if err != nil {
		fmt.Println("Err:", err)
		return
	}

	err = buf.WriteByte(ttl)
	if err != nil {
		fmt.Println("Err:", err)
		return
	}

	err = buf.WriteByte(hops)
	if err != nil {
		fmt.Println("Err:", err)
		return
	}

	err = binary.Write(buf, binary.BigEndian, int32(len(payload)))
	if err != nil {
		fmt.Println("Err:", err)
		return
	}

	n, err = buf.Write(payload)
	if n != len(payload) || err != nil {
		fmt.Println("Err:", err)
		return
	}

	b = buf.Bytes()

	return
}
