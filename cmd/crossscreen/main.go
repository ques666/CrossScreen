package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"crossscreen/core/protocol"
	"crossscreen/platform/adapter"
	"crossscreen/platform/capture"
	"crossscreen/platform/clipboard"
	"crossscreen/platform/config"
	"crossscreen/platform/control"
	"crossscreen/platform/filexfer"
	"crossscreen/platform/inject"
	"crossscreen/platform/tray"
	"crossscreen/platform/winsec"
)

// Double-clicking the packaged app starts the panel: an embedded HTTP server
// serves the browser control panel, the default browser opens automatically,
// and a status-bar icon exposes "open page / auto-start / quit". -headless
// keeps the old CLI node for scripts and debugging.
func main() {
	headless := flag.Bool("headless", false, "run a node without the control panel and status-bar icon")
	_ = flag.Bool("ui", false, "deprecated: the control panel is the default mode now")
	uiAddr := flag.String("uiaddr", "", "control panel listen address (default 127.0.0.1:<ui_port> from settings)")
	mode := flag.String("mode", "server", "headless role: server or client")
	addr := flag.String("addr", "", "listen address (server) or server address (client)")
	name := flag.String("name", "", "device name (default: hostname)")
	noCapture := flag.Bool("no-capture", false, "disable input capture and injection")
	noBrowser := flag.Bool("no-browser", false, "do not auto-open the browser")
	noAdmin := flag.Bool("no-admin", false, "do not self-elevate on Windows (then elevated windows like Task Manager cannot be controlled)")
	flag.Parse()

	// Windows-only internal modes: the SYSTEM service ("svc") and the
	// secure-desktop injection agent ("agent"). Must be handled before DPI
	// setup / elevation and before the panel starts.
	if winsec.HandleCommand(os.Args) {
		return
	}

	// Windows: DPI awareness + optional self-elevation. When the relaunch
	// happens, the new instance runs the same command and this one exits.
	if inject.Startup(!*noAdmin) {
		return
	}

	if !*headless {
		runPanel(*uiAddr, *noBrowser)
		return
	}
	runHeadless(*mode, *addr, *name, *noCapture)
}

// runPanel starts the embedded control panel, opens the browser and shows the
// status-bar icon. It blocks until the user quits.
func runPanel(uiAddrOverride string, noBrowser bool) {
	settings := config.Load()
	hostname, _ := os.Hostname()
	if settings.DeviceName == "" {
		settings.DeviceName = hostname
	}

	staging, err := config.StagingDir()
	if err != nil {
		log.Printf("crossscreen: file staging dir unavailable: %v", err)
	}
	files := filexfer.New(filexfer.Options{
		Self:       settings.DeviceName,
		StagingDir: staging,
	})
	files.CleanupStaging(7 * 24 * time.Hour)

	app := control.New(control.Config{
		NodeAddr: settings.ListenAddr,
		Settings: settings,
		Files:    files,
	})

	listenAddr := uiAddrOverride
	if listenAddr == "" {
		listenAddr = fmt.Sprintf("127.0.0.1:%d", settings.UIPort)
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// Configured port is busy (another instance, another app): fall back
		// to an ephemeral port rather than failing the double-click launch.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatalf("control panel listen: %v", err)
		}
	}
	srv := &http.Server{Handler: app.Handler()}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("control panel: %v", err)
		}
	}()

	panelURL := "http://" + ln.Addr().String()
	fmt.Printf("[crossscreen] control panel: %s\n", panelURL)
	if runtime.GOOS == "darwin" {
		fmt.Println("[hint] macOS: grant Input Monitoring + Accessibility to CrossScreen")
	}

	var stopOnce sync.Once
	shutdown := func() {
		stopOnce.Do(func() {
			app.StopNode()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
	}

	if !noBrowser {
		go func() {
			if err := openBrowser(panelURL); err != nil {
				fmt.Printf("[crossscreen] could not open browser automatically: %v\n", err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		shutdown()
		os.Exit(0)
	}()

	// Blocks on the native status-bar loop (must run on the main goroutine).
	tray.Run(tray.Options{
		PageURL: panelURL,
		OnOpenPage: func() {
			if err := openBrowser(panelURL); err != nil {
				fmt.Printf("[crossscreen] open page: %v\n", err)
			}
		},
		OnQuit: shutdown,
	})
	shutdown()
	os.Exit(0)
}

func runHeadless(mode, addr, name string, noCapture bool) {
	hostname, _ := os.Hostname()
	if name == "" {
		name = hostname
	}

	var role protocol.Role
	switch mode {
	case "server":
		role = protocol.RoleServer
	case "client":
		role = protocol.RoleClient
	default:
		log.Fatalf("unknown mode %q (want server or client)", mode)
	}
	hello := &protocol.Hello{Hostname: name, OS: runtimeOS(), Role: role, Version: "0.4.0"}

	var cap capture.Capture
	var err error
	if !noCapture {
		if cap, err = capture.New(); err != nil {
			log.Fatalf("capture: %v", err)
		}
	}
	inj, err := inject.New()
	if err != nil {
		log.Fatalf("injector: %v", err)
	}
	clip, _ := clipboard.New()

	if role == protocol.RoleServer {
		d, err := adapter.RunServer(adapter.ServerConfig{Hello: hello, Capture: cap, Injector: inj, Clipboard: clip})
		if err != nil {
			log.Fatalf("adapter: %v", err)
		}
		if addr == "" {
			addr = "0.0.0.0:53317"
		}
		a, err := d.Listen(addr)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		fmt.Printf("[%s] server listening on %s (capture=%v)\n", hello.Hostname, a, !noCapture)
		if !noCapture && runtime.GOOS == "darwin" {
			fmt.Println("[hint] macOS: grant Input Monitoring + Accessibility to this terminal")
		}
		waitForSignal(func() error { return d.Close() })
		return
	}

	if addr == "" {
		log.Fatal("client mode requires -addr <server>")
	}
	d, err := adapter.RunClient(addr, adapter.ClientConfig{
		Hello:     hello,
		Capture:   cap,
		Injector:  inj,
		Clipboard: clip,
		OnDisconnect: func() {
			log.Printf("[%s] disconnected from server; resources released", hello.Hostname)
		},
	})
	if err != nil {
		log.Fatalf("adapter: %v", err)
	}
	fmt.Printf("[%s] client connected to %s\n", hello.Hostname, addr)
	waitForSignal(func() error { return d.Close() })
}

func waitForSignal(closeFn func() error) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	_ = closeFn()
}

func runtimeOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS
	}
}
