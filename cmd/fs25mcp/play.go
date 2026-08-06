package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// errNoGameCommand is returned when play was given nothing to launch.
var errNoGameCommand = errors.New(
	`play needs the game command after --, e.g. "fs25mcp play -- %command%"`)

// runPlay launches the game with the server running alongside it, and
// stops the server when the game exits.
//
// This is the companion-app shape, and it exists because "remember to
// start the server first" is not something anybody does twice. On Steam
// it goes in Launch Options and never has to be thought about again:
//
//	fs25mcp play -- %command%
//
// Steam substitutes %command% with the real launcher — Proton and all —
// so the game starts exactly as it would have. The server's lifetime is
// the play session's, which is also the only time its answers are worth
// anything: nobody asks what to plant while the game is shut.
//
// The alternative, a service that runs at login, is better when
// something OTHER than the player wants the state (an assistant on the
// LAN answering while the game is closed). Both are supported because
// they answer different questions; neither needs a config file.
func runPlay(ctx context.Context, src *source, args []string, addr, relayURL string) error {
	// Go's flag package hands back the "--" separator, and Steam's
	// %command% expands to the launcher after it.
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errNoGameCommand
	}

	server := newServer(src)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()

	if relayURL != "" {
		go func() {
			if err := serveConnect(serverCtx, handler, relayURL); err != nil && serverCtx.Err() == nil {
				log.Printf("fs25mcp: relay stopped: %v", err)
			}
		}()
		log.Printf("fs25mcp: relay %s, launching game", relayURL)
	} else {
		srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("fs25mcp: server stopped: %v", err)
			}
		}()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		}()
		log.Printf("fs25mcp: serving on http://%s, launching game", addr)
	}

	game := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // the command is the player's own launcher
	game.Stdout, game.Stderr, game.Stdin = os.Stdout, os.Stderr, os.Stdin

	// Steam kills the launcher, not us, so a terminating signal has to be
	// passed down or the game would be orphaned and keep running.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	if err := game.Start(); err != nil {
		return fmt.Errorf("launch game: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- game.Wait() }()

	select {
	case err := <-done:
		log.Printf("fs25mcp: game exited, stopping")
		return err
	case s := <-sig:
		if game.Process != nil {
			_ = game.Process.Signal(s)
		}
		<-done
		return nil
	}
}
