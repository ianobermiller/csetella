// Functions for sending and serializing meessages

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

func startSendLoop() {
	shouldPing := true
	for {
		for p, c := range peer2conn {
			if c == nil {
				connect(p)
			}
		}

		for c, _ := range conn2peer {
			if shouldPing {
				ping(c)
			} else {
				query(c)
			}
		}

		if len(conn2peer) > 0 {
			shouldPing = !shouldPing
		}
		time.Sleep(TIMER_INTERVAL)
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
		send(conn, msg)
	}
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
