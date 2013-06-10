// Handle command line params and start everything

package main

import (
	"./cache"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

var externalHost string
var externalPort int
var secret string
var knownAddress string
var logFile string

// Maps connections to their peer string (IP:PORT)
var conn2peer = make(map[net.Conn]string)

// Maps a peer string to their connection, which may be nil
var peer2conn = make(map[string]net.Conn)

// Map of secret texts we have seen this sessions, so we don't write them out again
var repliesSeen = make(map[string]CapturedReply)

// Cache of recent messages's we have seen, entries expire after 1 minute
// Key is msgId-type.
// Value is the peer string, so this is double duty as the routing table for incoming
//  pongs and replies.
var msgCache = cache.New(1*time.Minute, 30*time.Second)

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	flag.PrintDefaults()
}

func main() {
	flag.StringVar(&externalHost, "host", HOST, "external ip to send in pongs")
	flag.IntVar(&externalPort, "port", PORT, "external port to listen on and send in pongs")
	flag.StringVar(&secret, "secret", SECRET, "secret text to send in replies")
	flag.StringVar(&knownAddress, "seed", KNOWN_ADDRESS, "initial seed node to ping (format host:port)")
	flag.StringVar(&logFile, "log", LOG_FILE, "log file to which secrets are written")

	flag.Usage = Usage
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix(logFile + " ")

	go startWebServer()
	go startListener()

	tryAddPeerString(knownAddress)

	startSendLoop()
}

func tryAddPeer(ipBytes []byte, port uint16) {
	ip := net.IP(ipBytes).String()

	if ip == externalHost && int(port) == externalPort {
		// don't connect to self
		return
	}

	peer := fmt.Sprintf("%v:%v", ip, port)
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
