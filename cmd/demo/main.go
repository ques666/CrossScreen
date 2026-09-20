// Command demo starts a local session server, connects a client, routes a
// pointer move across the virtual layout boundary and prints the events.
//
// Run with: go run ./cmd/demo
package main

import (
	"fmt"
	"log"
	"time"

	"crossscreen/core/event"
	"crossscreen/core/layout"
	"crossscreen/core/protocol"
	"crossscreen/core/session"
)

func main() {
	serverHello := &protocol.Hello{Hostname: "mac-demo", OS: "macos", Role: protocol.RoleServer, Version: "0.2.0"}
	srv := session.NewServer(serverHello, func(env *protocol.Envelope) error {
		fmt.Printf("[server] local sink received: %s\n", env.Type)
		return nil
	})
	addr, err := srv.Listen("127.0.0.1:0", nil)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	fmt.Printf("[server] listening on %s\n", addr)

	clientHello := &protocol.Hello{Hostname: "win-demo", OS: "windows", Role: protocol.RoleClient, Version: "0.2.0"}
	cl, err := session.Dial(addr.String(), clientHello, 3*time.Second)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	cl.SetSink(func(env *protocol.Envelope) error {
		fmt.Printf("[client] received: %s\n", env.Type)
		if env.Type == protocol.TypePointerMove {
			var m event.PointerMove
			if err := env.Decode(&m); err == nil {
				fmt.Printf("[client] pointer move at virtual (%d, %d)\n", m.X, m.Y)
			}
		}
		return nil
	})
	go func() { _ = cl.Serve() }()

	if err := cl.SendLayout([]*layout.Screen{
		{ID: "win-demo:0", Device: "win-demo", Name: "Win", Width: 1280, Height: 800},
	}); err != nil {
		log.Fatalf("send layout: %v", err)
	}

	arr := &layout.Layout{Version: 1, Screens: []*layout.Screen{
		{ID: "mac-demo:0", Device: "mac-demo", Name: "Mac", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		{ID: "win-demo:0", Device: "win-demo", Name: "Win", X: 1920, Y: 0, Width: 1280, Height: 800},
	}}
	if err := srv.SetLayout(arr); err != nil {
		log.Fatalf("set layout: %v", err)
	}

	mac := arr.Screens[0]
	fmt.Println("[server] moving pointer to the right edge, then crossing into win-demo...")
	if err := srv.OnLocalPointerMove(mac, 1919, 400); err != nil {
		log.Fatalf("move: %v", err)
	}
	if err := srv.OnLocalPointerMove(mac, 1921, 400); err != nil {
		log.Fatalf("move: %v", err)
	}
	if err := srv.OnLocalButton(event.PointerButton{Button: event.ButtonLeft, Down: true}); err != nil {
		log.Fatalf("button: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	fmt.Println("done")
}
