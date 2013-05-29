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

func main() {

	messageId, err := genId()
	if err != nil {
		fmt.Printf("Error generating message id:", err)
	}

	b, err := buildMessage(messageId, Ping, 100, 0, []byte{})
	if err != nil {
		return
	}

	conn, err := net.Dial("tcp", "128.208.2.88:5002")
	if err != nil {
		fmt.Printf("Dial err:", err)
		return
	}

	n, err := conn.Write(b)
	if n != len(b) || err != nil {
		fmt.Printf("Err writing to conn: ", err)
		return
	}

	for {
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		if n == 0 {
			continue
		}
		if err != nil {
			fmt.Printf("ReadFull err:", err)
		}

		fmt.Printf("% x\n", buf[:n])
	}
}

func genId() (messageId []byte, err error) {
	messageId = make([]byte, 16)
	_, err = io.ReadFull(rand.Reader, messageId)
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
		fmt.Printf("Err:", err)
		return
	}

	err = buf.WriteByte(byte(messageType))
	if err != nil {
		fmt.Printf("Err:", err)
		return
	}

	err = buf.WriteByte(ttl)
	if err != nil {
		fmt.Printf("Err:", err)
		return
	}

	err = buf.WriteByte(hops)
	if err != nil {
		fmt.Printf("Err:", err)
		return
	}

	err = binary.Write(buf, binary.BigEndian, int32(len(payload)))
	if err != nil {
		fmt.Printf("Err:", err)
		return
	}

	n, err = buf.Write(payload)
	if n != len(payload) || err != nil {
		fmt.Printf("Err:", err)
		return
	}

	fmt.Printf("% x\n", buf.Bytes())

	b = buf.Bytes()

	return
}
