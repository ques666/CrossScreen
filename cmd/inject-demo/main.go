// Command inject-demo enumerates the local displays and, when run with the
// -move or -key flags, injects a test input event.
//
// Note: on macOS, input injection requires Accessibility permission for the
// terminal/IDE that runs this program. Run with:
//
//	go run ./cmd/inject-demo            # list screens only
//	go run ./cmd/inject-demo -move      # move the pointer to screen center
//	go run ./cmd/inject-demo -key A     # press and release the A key
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"crossscreen/core/keymap"
	"crossscreen/core/layout"
	"crossscreen/platform/inject"
)

func main() {
	move := flag.Bool("move", false, "inject a test pointer move to the primary screen center")
	key := flag.String("key", "", "inject a key press by name (e.g. A, Enter, LeftShift)")
	flag.Parse()

	screens, err := inject.EnumerateScreens(layout.DeviceID("demo"))
	if err != nil {
		log.Fatalf("enumerate screens: %v", err)
	}
	if len(screens) == 0 {
		log.Fatal("no displays found")
	}
	for _, s := range screens {
		marker := " "
		if s.Primary {
			marker = "*"
		}
		fmt.Printf("%s %s  %s  (%d,%d) %dx%d\n", marker, s.ID, s.Name, s.X, s.Y, s.Width, s.Height)
	}

	inj, err := inject.New()
	if err != nil {
		log.Fatalf("injector: %v", err)
	}
	if *move {
		primary := screens[0]
		for _, s := range screens {
			if s.Primary {
				primary = s
			}
		}
		x, y := primary.X+primary.Width/2, primary.Y+primary.Height/2
		fmt.Printf("moving pointer to (%d, %d)\n", x, y)
		if err := inj.MovePointer(x, y); err != nil {
			log.Fatalf("move: %v", err)
		}
	}
	if *key != "" {
		k, ok := keymap.KeyByName(*key)
		if !ok {
			log.Fatalf("unknown key %q", *key)
		}
		fmt.Printf("pressing %s\n", *key)
		if err := inj.Key(k, true); err != nil {
			log.Fatalf("key down: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		if err := inj.Key(k, false); err != nil {
			log.Fatalf("key up: %v", err)
		}
	}
	fmt.Println("done")
}
