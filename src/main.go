package main

import (
	"./cache"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	mrand "math/rand"
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

type Conn struct {
	conn      *net.Conn
	gotSecret bool
}

const KNOWN_ADDRESS = "128.208.2.88:5002"
const HOST = "128.208.1.137"
const PORT = 25610
const SECRET = "Ian Obermiller -- iano [at] cs.washington.edu -- 25610"
const LOG_FILE = "log.txt"

var host string
var port int
var secret string
var knownAddress string
var logFile string

var peers = make([]string, 0)
var connections = make(map[string]*Conn)
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

	peers = append(peers, knownAddress)

	for {
		// choose a random peer and try to connect to them
		var err error
		index := 0
		if len(peers) > 0 {
			index = mrand.Intn(len(peers))
			if err != nil {
				fmt.Println("Rand err:", err)
				continue
			}
		}
		peer := peers[index]
		c, ok := connections[peer]
		if ok {
			if c.gotSecret {
				go ping(*c.conn)
			} else {
				go query(*c.conn)
			}
		} else {
			connect(peer)
		}
		time.Sleep(2 * time.Second)
	}
}

func connect(address string) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Println("Dial err:", err)
		return
	}

	go handleConnection(conn)

	go ping(conn)
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
		go handleConnection(conn)
	}
}

func handleConnection(c net.Conn) {
	addr := c.RemoteAddr().String()
	connections[addr] = &Conn{&c, false}

	b := make([]byte, 2048)
	for {
		n, err := c.Read(b)

		if err != nil {
			fmt.Println("Read err:", err)
			delete(connections, addr)
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
	t := MessageType(b[16])
	msgId := b[:16]
	fmt.Printf("RECV %s %s - % x\n", t, c.RemoteAddr(), b)

	key := getKey(msgId, t)
	if _, found := msgCache.Get(key); found {
		return
	}

	msgCache.Set(key, true, 0)

	switch t {
	case Ping:
		go pong(c, msgId)
	case Query:
		go reply(c, msgId)
	case Pong:
		go processPong(c, b)
	case Reply:
		go processReply(c, b)
	}
}

func processPong(c net.Conn, b []byte) {
	buf := bytes.NewBuffer(b[24:])
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
