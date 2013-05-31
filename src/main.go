package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
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

const KNOWN_ADDRESS = "128.208.2.88:5002"
const HOST = "128.208.1.137"
const PORT = 25610
const SECRET = "Ian Obermiller <itao@uw.edu>"
const LOG_FILE = "log.txt"

var host string
var port int
var secret string
var knownAddress string
var logFile string

func main() {
	flag.StringVar(&host, "host", HOST, "external ip to send in pongs")
	flag.IntVar(&port, "port", PORT, "external port to send in pongs")
	flag.StringVar(&secret, "secret", SECRET, "secret text to send in replies")
	flag.StringVar(&knownAddress, "seed", KNOWN_ADDRESS, "initial seed node to ping (format host:port)")
	flag.StringVar(&logFile, "log", LOG_FILE, "log file to which secrets are written")

	connect(knownAddress)
	startListener()
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
	binary.Write(buf, binary.BigEndian, int16(port))
	buf.Write(net.ParseIP(host).To4())
	send(c, Pong, pingMessageId, buf.Bytes())
}

func query(c net.Conn) {
	send(c, Query, genId(), []byte{})
}

func reply(c net.Conn, queryMessageId []byte) {
	send(c, Query, queryMessageId, []byte(secret))
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
	for {
		b := make([]byte, 2048)
		n, err := c.Read(b)

		if err != nil {
			fmt.Println("Read err:", err)
		}

		if n == 0 {
			continue
		}

		t := MessageType(b[16])
		msgId := b[:16]
		fmt.Printf("RECV %s %s - % x\n", t, c.RemoteAddr(), b[:n])

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
}

func processPong(c net.Conn, b []byte) {
	buf := bytes.NewBuffer(b[24:])
	var port int16
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
	buf := bytes.NewBuffer(b[20:])
	var size int32
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		fmt.Println("Could not read size from Reply message: ", err)
		return
	}

	secretTextBytes := make([]byte, size)
	n, err := buf.Read(secretTextBytes)
	if n != int(size) || err != nil {
		fmt.Println("Could not read secret text from Reply message: ", err)
		return
	}

	log := fmt.Sprint("Address", c.RemoteAddr().String(), "sent secret text", string(secretTextBytes), "\n")
	fmt.Print(log)
	file, _ := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE, 0666)
	file.WriteString(log)
	file.Sync()
	file.Close()
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
