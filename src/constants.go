// Constants, defaults, and structs

package main

import (
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
	TIMER_INTERVAL     = 5 * time.Second
	CONNECTION_TIMEOUT = 3 * time.Second
	TTL                = 10
	KNOWN_ADDRESS      = "128.208.2.88:5002"
	HOST               = "128.208.1.139"
	PORT               = 20202
	SECRET             = "Ian Obermiller -- iano [at] cs.washington.edu -- 47249046"
	LOG_FILE           = "log.txt"
)

type Message struct {
	MsgId   []byte
	MsgType MessageType
	TTL     byte
	Hops    byte
	Payload []byte
}

func (msg *Message) Key() string {
	return getKey(msg.MsgId, msg.MsgType)
}

type CapturedReply struct {
	FromPeer string
	TimeSeen time.Time
	Text     string
}
