package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// from https://is.xivup.com/adv
var servers = map[string]string{
	"aether":  "204.2.229.9",  // neolobby02.ffxiv.com
	"primal":  "204.2.229.10", // neolobby04.ffxiv.com
	"crystal": "204.2.229.11", // neolobby08.ffxiv.com

	"chaos": "195.82.50.9",  // neolobby06.ffxiv.com
	"light": "195.82.50.10", // neolobby07.ffxiv.com

	"elemental": "124.150.157.158", // neolobby01.ffxiv.com
	"gaia":      "124.150.157.157", // neolobby03.ffxiv.com
	"mana":      "124.150.157.156", // neolobby05.ffxiv.com
}

func main() {
	listenAddrStr := flag.String("listen", "127.0.1.1:54994", "listen address")
	serverAddrStr := flag.String("server", "aether", "server name or IP address")

	flag.Parse()

	var serverAddr = *serverAddrStr

	if net.ParseIP(serverAddr) == nil {
		serverAddr = servers[strings.ToLower(serverAddr)]
	}

	if serverAddr == "" {
		log.Fatalln("Invalid server address")
	}

	ln, err := net.Listen("tcp", *listenAddrStr)
	if err != nil {
		log.Fatalln(err)
	}

	log.Printf("Ready. Listening on: %s. Connecting to: %s\n", ln.Addr().String(), serverAddr)

	n := 0
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		n++
		go handleConnection(conn, serverAddr, n)
	}
}

func handleConnection(local net.Conn, serverAddr string, n int) {
	defer local.Close()

	log.Printf("[%d] %s %s\n", n, "accept", local.RemoteAddr().String())

	var remote net.Conn
	buf := make([]byte, 128*1024)
	serverAddr = fmt.Sprintf("%s:54994", serverAddr)
	tries := 0

	op := func() error {
		tries++
		var err error
		remote, err = net.Dial("tcp", serverAddr)
		if err != nil {
			log.Printf("[%d] %s\n", n, "initial remote conn failed")
			return err
		}

		log.Printf("[%d] %s\n", n, "initial remote conn ok")

		remote.SetReadDeadline(time.Now().Add(1 * time.Second))

		c, err := remote.Read(buf)

		log.Printf("[%d] %s %d\n", n, "initial read bytes", c)

		// conn failed
		if err != nil {
			log.Printf("[%d] %s\n", n, "remote failed early")
			remote.Close()
			remote = nil

			return errors.New("remote failed early")
		}

		buf = buf[0:c]
		_, err = local.Write(buf)
		if err != nil {
			log.Printf("[%d] %s\n", n, "initial write to local failed")
			remote.Close()
			remote = nil

			return backoff.Permanent(err)
		}

		log.Printf("[%d] %s\n", n, "remote ok")
		return nil
	}

	exp := backoff.NewExponentialBackOff()
	exp.MaxInterval = time.Second

	err := backoff.Retry(op, exp)

	if err != nil {
		log.Printf("[%d] %s %s\n", n, "permanent fail", err.Error())
		return
	}

	if remote == nil {
		log.Printf("[%d] %s\n", n, "permanent fail - unknown error")
		return
	}

	if tries > 1 {
		log.Printf("[%d] %s (prevented 2002!)\n", n, "ok, starting passthrough")
	} else {
		log.Printf("[%d] %s\n", n, "ok, starting passthrough")
	}

	dead := make(chan bool, 2)
	halt := make(chan bool, 1)

	go relay(n, local, remote, dead, halt)
	go relay(n, remote, local, dead, halt)

	<-dead

	close(halt)

	<-dead

	log.Printf("[%d] %s\n", n, "shutdown")

	remote.Close()
}

func relay(n int, a net.Conn, b net.Conn, dead chan<- bool, halt <-chan bool) {
	defer func() {
		dead <- true
	}()

	buf := make([]byte, 128*1024)
	for {
		select {
		case <-halt:
			return
		default:
			a.SetReadDeadline(time.Now().Add(1 * time.Second))
			c, err := a.Read(buf)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					continue
				} else {
					log.Printf("[%d] %s->%s %s\n", n, a.RemoteAddr().String(), b.RemoteAddr().String(), "read dead")
					return
				}
			}

			xbuf := buf[0:c]
			_, err = b.Write(xbuf)
			if err != nil {
				log.Printf("[%d] %s->%s %s\n", n, a.RemoteAddr().String(), b.RemoteAddr().String(), "write dead")
				return
			}
		}
	}
}
