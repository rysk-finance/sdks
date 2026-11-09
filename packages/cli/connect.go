package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

var connectAction = &Command{
	Name:  "connect",
	Usage: "instantiate a websocket connection, sending the commands written to stdin",
	Flags: []Flag{
		&StringFlag{
			Name:     "url",
			Required: true,
			Usage:    "ws url to connect to",
		},
	},
	Action: connect,
}

// send is where the channel commands put their payload. Run on their own they
// print it, so a signed message can be inspected or piped; inside connect it
// goes to the websocket.
var send = func(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("invalid payload: %v", err)
	}
	fmt.Println(string(data))
	return nil
}

// channelApp runs the commands connect reads from stdin: only the ones that
// write to the websocket.
var channelApp = &Command{
	Name: "ryskV12",
	Subcommands: []*Command{
		// maker
		balancesAction, positionsAction, quoteAction, transferAction, willRedeemAction,
		// taker
		inventoryAction, requestAction, confirmAction,
	},
}

func connect(c *Context) error {
	ctx, cancel := context.WithCancel(c.Context)
	connectURL = c.String("url")
	conn, _, err := websocket.DefaultDialer.Dial(connectURL, nil)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	conn.SetCloseHandler(func(code int, text string) error {
		log.Print("connection closed by peer")
		cancel()
		return nil
	})
	// runCommands is the only goroutine that calls send, so this is the
	// connection's single writer; pongs go out through gorilla's default ping
	// handler, whose WriteControl is safe alongside it.
	send = func(payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("invalid payload: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Fatal(err)
		}
		return nil
	}
	// stdin closing only stops commands; the connection stays up until it is
	// killed or the server closes it, so a listen-only connect can run with
	// stdin at /dev/null.
	go runCommands(os.Stdin)
	go listenMessages(conn)
	<-ctx.Done()
	return nil
}

// runCommands runs each line of r - a json array of args, as they would follow
// `ryskV12` on the command line - in order, in this process.
func runCommands(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var args []string
		if err := json.Unmarshal(scanner.Bytes(), &args); err != nil || len(args) == 0 {
			log.Printf("ignoring command %q: want a json array of args", scanner.Text())
			continue
		}
		if err := channelApp.Run(context.Background(), args); err != nil {
			// reported like the server's own errors, under the id the server
			// would have answered with, so the invoker handles both the same way
			out, _ := json.Marshal(JsonRPCResponse{
				JsonRPC: "2.0",
				ID:      commandID(args),
				Error:   &ErrorData{Code: -32602, Message: fmt.Sprintf("%s: %v", args[0], err)},
			})
			printLine(out)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error reading commands: %v", err)
	}
}

// printLine writes one message per stdout line, which is how the invoker tells
// messages apart: json is compacted so a pretty printed message cannot span
// lines.
func printLine(msg []byte) {
	var line bytes.Buffer
	if json.Compact(&line, msg) != nil {
		line.Reset()
		line.Write(bytes.ReplaceAll(msg, []byte("\n"), []byte(" ")))
	}
	fmt.Println(line.String())
}

// commandID is the id the server answers a command under: the rfq for a quote,
// the method otherwise.
func commandID(args []string) string {
	for i, arg := range args {
		name, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name != "rfq_id" {
			continue
		}
		if inline {
			return value
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return args[0]
}

func listenMessages(conn *websocket.Conn) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Fatalf("Read error: %s", err.Error())
		}
		printLine(msg)
	}
}
