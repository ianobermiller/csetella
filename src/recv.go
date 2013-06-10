// Functions for handling connections, receiving, and parsing messages

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"
)

func connect(address string) {
	log.Println("Connecting to", address)
	conn, err := net.DialTimeout("tcp", address, CONNECTION_TIMEOUT)
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

func handleConnection(c net.Conn) {
	peer := c.RemoteAddr().String()
	conn2peer[c] = peer

	fmt.Println("Connected to ", peer)

	if _, ok := peer2conn[peer]; ok {
		peer2conn[peer] = c
	}

	// Repeatedly read a message from the connection.
	// If anything is awry, close the connection.
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

		if ttl > 100 {
			err = errors.New(fmt.Sprint("TTL", ttl, "was unexpectedly large."))
			break
		}

		hops, err := readByte(c)
		if err != nil {
			break
		}

		if hops > 100 {
			err = errors.New(fmt.Sprint("Hops", hops, "was unexpectedly large."))
			break
		}

		var size int32
		err = binary.Read(c, binary.BigEndian, &size)
		if err != nil {
			break
		}

		if size > 1000 {
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

func getKey(msgId []byte, mt MessageType) string {
	return fmt.Sprintf("% x - %v", msgId, mt)
}

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
		processPing(c, msg)
	case Query:
		processQuery(c, msg)
	case Pong:
		processPong(c, msg)
	case Reply:
		processReply(c, msg)
	}
}

func processPing(c net.Conn, msg *Message) {
	pong(c, msg.MsgId)
	forwardMessage(c, msg, true)
}

func processQuery(c net.Conn, msg *Message) {
	reply(c, msg.MsgId)
	forwardMessage(c, msg, true)
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

	secretText := string(trimNullBytesFromEnd(msg.Payload[6:]))

	key := getKey(msg.MsgId, Reply)
	if peer, found := msgCache.Get(key); found {
		// Send the reply back to the original querier
		if conn, found := peer2conn[peer.(string)]; found {
			forwardMessage(conn, msg, false)
		}
	}

	peer := fmt.Sprintf("%v:%v", net.IP(ipBytes).String(), port)

	logMsg := fmt.Sprintf("Peer %v sent secret text \"%v\" (size %v)\n", peer, secretText, len(msg.Payload)-6)

	rep := CapturedReply{
		FromPeer: peer,
		TimeSeen: time.Now(),
		Text:     secretText}

	replyKey := peer + "-" + secretText
	if _, ok := repliesSeen[replyKey]; ok {
		return
	}

	repliesSeen[replyKey] = rep

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

func trimNullBytesFromEnd(b []byte) []byte {
	for i, by := range b {
		if by == 0 {
			return b[0:i]
		}
	}
	return b
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
