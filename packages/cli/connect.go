package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v2"
)

var connectAction = &cli.Command{
	Name:  "connect",
	Usage: "instantiate a websocket connection",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "channel_id",
			Required: true,
			Usage:    "a unique id so you can later interact with this specific websocket",
		},
		&cli.StringFlag{
			Name:     "url",
			Required: true,
			Usage:    "ws url to connect to",
		},
	},
	Action: func(c *cli.Context) error {
		return connect(c)
	},
}

func connect(c *cli.Context) error {
	channel_id := c.String("channel_id")
	socketPath := fmt.Sprintf("/tmp/%s.sock", channel_id)
	cmdChan := make(chan []byte)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	defer ln.Close()
	defer os.Remove(socketPath)

	ctx, cancel := context.WithCancel(c.Context)
	// ws connection
	conn, _, err := websocket.DefaultDialer.Dial(c.String("url"), nil)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	client := NewClient(ctx, cancel, conn)
	client.SetHandler(func(msg []byte) {
		fmt.Println(string(msg))
	})
	go listenMessages(client)
	go pipeCommands(ctx, ln, cmdChan)

	for {
		select {
		case <-ctx.Done():
			return nil
		case cmd := <-cmdChan:
			if strings.Contains(string(cmd), "disconnect") {
				cancel()
				return nil
			} else {
				client.Send(cmd)
			}
		}
	}
}

func listenMessages(client *Client) {
	for {
		select {
		case <-client.Ctx.Done():
			log.Print("connection closed by peer")
			return
		default:
			_, msg, err := client.Connection.ReadMessage()
			if err != nil {
				log.Fatalf("Read error: %s", err.Error())
			}
			client.Ingest(msg)
		}
	}
}

func writeToSocket(channelID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("invalid payload: %v", err)
	}

	socketPath := fmt.Sprintf("/tmp/%s.sock", channelID)
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed to connect to socket: %v", err)
	}
	defer conn.Close()

	_, err = conn.Write(append(data, '\n')) // Append newline for line-based reading.
	if err != nil {
		return fmt.Errorf("failed to write to socket: %v", err)
	}
	return nil
}

func pipeCommands(ctx context.Context, ln *net.UnixListener, ch chan<- []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			unixConn, err := ln.AcceptUnix()
			if err != nil {
				log.Fatalf("accept error: %v", err)
			}
			scanner := bufio.NewScanner(unixConn)
			for scanner.Scan() {
				cmd := scanner.Bytes()
				ch <- cmd
			}
			if err := scanner.Err(); err != nil {
				log.Printf("Error reading from socket: %v", err)
			}
			unixConn.Close()
		}
	}

}
