package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	listenAddress     = ":50051"
	connectionTimeout = 10 * time.Second
)

func main() {
	listenerConfig := net.ListenConfig{}
	listener, err := listenerConfig.Listen(context.Background(), "tcp", listenAddress)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go echo(conn)
	}
}

func echo(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.Print(err)
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(connectionTimeout)); err != nil {
		log.Print(err)
		return
	}
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		if _, err := fmt.Fprintln(conn, scanner.Text()); err != nil {
			log.Print(err)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Print(err)
	}
}
