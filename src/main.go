package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
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

const HOST = "128.208.1.137"
const PORT int16 = 25610

func main() {
	host := "128.208.2.88:5002"

	conn, err := net.Dial("tcp", host)
	if err != nil {
		fmt.Println("Dial err:", err)
		return
	}

	go handleConnection(conn)

	go ping(conn)
	startListener()
}

func ping(c net.Conn) {
	send(c, Ping, genId(), []byte{})
}

func pong(c net.Conn, pingMessageId []byte) {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, PORT)
	buf.Write(net.ParseIP(HOST).To4())
	send(c, Pong, pingMessageId, buf.Bytes())
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
	ln, err := net.Listen("tcp", fmt.Sprint(":", PORT))
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
		buf := make([]byte, 2048)
		n, err := c.Read(buf)

		if err != nil {
			fmt.Println("Read err:", err)
		}

		if n == 0 {
			continue
		}

		t := MessageType(buf[16])
		fmt.Printf("RECV %s %s - % x\n", t, c.RemoteAddr(), buf[:n])

		switch t {
		case Ping:
			go pong(c, buf[:16])
		}
	}
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
