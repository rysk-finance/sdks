package main

import (
	"context"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	Connection *websocket.Conn
	Ctx        context.Context
	Disconnect context.CancelFunc
	in         chan []byte
	lastPong   time.Time
	out        chan any
	handler    func([]byte)
}

func NewClient(ctx context.Context, kill context.CancelFunc, conn *websocket.Conn) *Client {
	c := Client{
		Connection: conn,
		Ctx:        ctx,
		Disconnect: kill,
		in:         make(chan []byte),
		lastPong:   time.Now(),
		out:        make(chan any),
		handler: func([]byte) {
			panic("no handler set")
		},
	}

	conn.SetPingHandler(func(data string) error {
		c.out <- websocket.PongMessage
		return nil
	})

	conn.SetCloseHandler(func(code int, text string) error {
		log.Print("connection closed by peer")
		kill()
		return nil
	})
	// process outbound queue
	go c.processOutboundMsgs()
	// go process inbound queue
	go c.processInboudMsgs()
	return &c
}

/*
Set handler must be called before any inbound message is received or the application will panic
*/
func (c *Client) SetHandler(handler func([]byte)) {
	c.handler = handler
}

func (c *Client) Ingest(req []byte) {
	c.in <- req
}

func (c *Client) Send(res []byte) {
	c.out <- res
}

func (c *Client) processOutboundMsgs() {
	for {
		select {
		case <-c.Ctx.Done():
			return
		case msg := <-c.out:
			switch msg := msg.(type) {
			case int: // pongmessage = int
				err := c.Connection.WriteMessage(websocket.PongMessage, []byte{})
				if err != nil {
					log.Fatal(err)
				}
			case []byte:
				err := c.Connection.WriteMessage(websocket.TextMessage, msg)
				if err != nil {
					log.Fatal(err)
				}
			default:
				log.Fatal("unknown message type to send")
			}

		}
	}
}

func (c *Client) processInboudMsgs() {
	for {
		select {
		case <-c.Ctx.Done():
			return
		case req := <-c.in:
			c.handler(req)
		}
	}
}
