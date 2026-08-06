// fs25mcp serves your Farming Simulator 25 install and savegame over MCP.
//
// It is built to need no configuration and no LLM to set up. Run it with
// no arguments and it finds the game, finds the savegame you played most
// recently, and starts serving. `fs25mcp status` prints what it found in
// plain text, so the whole chain — game, server, relay — can be verified
// by eye before any model is involved. The model is the consumer at the
// end of the pipe, never a dependency of the plumbing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pequalsnp/fs25mcp/internal/fs25data"
	"github.com/pequalsnp/fs25mcp/internal/fs25save"
	"github.com/pequalsnp/fs25mcp/internal/locate"
)

const usage = `fs25mcp — Farming Simulator 25 over MCP

  fs25mcp                 serve (auto-detects game and savegame)
  fs25mcp status          print what was found, and exit
  fs25mcp -h              flags

Nothing needs configuring. Point -install/-save at them only if the
auto-detection misses, which it will on a Steam library that lives on
another drive.
`

func main() {
	var (
		installDir = flag.String("install", "", "game install directory (default: auto-detect)")
		saveDir    = flag.String("save", "", "savegame directory (default: auto-detect)")
		savegame   = flag.String("savegame", "", "which savegame, e.g. savegame4 (default: most recently played)")
		addr       = flag.String("addr", "127.0.0.1:14005", "listen address for MCP over HTTP")
		relayURL   = flag.String("relay", "", "optional: dial OUT to a relay instead of listening, e.g. http://host:8091/relay/fs25")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	flag.Parse()

	src, err := resolve(*installDir, *saveDir, *savegame)
	if err != nil {
		log.Fatal(err)
	}

	if flag.Arg(0) == "status" {
		src.printStatus()
		return
	}

	server := newServer(src)
	ctx := context.Background()

	if *relayURL != "" {
		log.Printf("fs25mcp: dialing relay %s", *relayURL)
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
		log.Fatal(serveConnect(ctx, handler, *relayURL))
	}

	log.Printf("fs25mcp: serving %s (%s) on http://%s", src.Save.Name, src.Install.Version, *addr)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// source is everything the tools read: the install, and one savegame.
type source struct {
	Install     *fs25data.Install
	Save        *fs25save.Save
	SaveName    string
	SaveDir     string
	InstallPath string
}

func resolve(installDir, saveDir, savegame string) (*source, error) {
	var err error
	if installDir == "" {
		if installDir, err = locate.Install(); err != nil {
			return nil, err
		}
	}
	if saveDir == "" {
		if saveDir, err = locate.SaveDir(); err != nil {
			return nil, err
		}
	}

	in, err := fs25data.Parse(installDir)
	if err != nil {
		return nil, fmt.Errorf("read install %s: %w", installDir, err)
	}

	var chosen locate.Savegame
	if savegame != "" {
		saves, err := locate.Savegames(saveDir)
		if err != nil {
			return nil, err
		}
		for _, s := range saves {
			if s.Name == savegame {
				chosen = s
			}
		}
		if chosen.Dir == "" {
			return nil, fmt.Errorf("no %s in %s", savegame, saveDir)
		}
	} else if chosen, err = locate.Latest(saveDir); err != nil {
		return nil, err
	}

	sv, err := fs25save.Read(chosen.Dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", chosen.Name, err)
	}

	return &source{
		Install: in, Save: sv, SaveName: chosen.Name,
		SaveDir: saveDir, InstallPath: installDir,
	}, nil
}

// reload re-reads the savegame. The file is only as fresh as the last
// time the player saved, so a tool that answers from a five-hour-old
// save without saying so is worse than one that refuses.
func (s *source) reload() error {
	sv, err := fs25save.Read(s.Save.Dir)
	if err != nil {
		return err
	}
	s.Save = sv
	return nil
}

// printStatus is the no-LLM verification path: run it and see whether the
// plumbing is right.
func (s *source) printStatus() {
	mods := 0
	for _, p := range s.Save.Productions {
		if p.Mod != "" {
			mods++
		}
	}
	fmt.Printf(`fs25mcp status

  install        %s
                 version %s, %d store items, %d crops, %d fill types, %d production points
  savegames      %s
  using          %s — %q on %s%s

  farm           %s
  money          %.0f (loan %.0f)
  farmland       %d of %d owned
  vehicles       %d
  productions    %d owned, %d of them from mods
  played         %.0f hours, last saved %s
`,
		s.InstallPath, s.Install.Version, len(s.Install.StoreItems), len(s.Install.Crops),
		len(s.Install.FillTypes), len(s.Install.Factories),
		s.SaveDir,
		s.SaveName, s.Save.Name, s.Save.Map, modSuffix(s.Save.MapIsMod),
		s.Save.FarmName, s.Save.Money, s.Save.Loan,
		s.Save.FarmlandOwned, s.Save.FarmlandTotal, s.Save.Vehicles,
		len(s.Save.Productions), mods,
		s.Save.PlayTimeHours, s.Save.SaveDate)
}

func modSuffix(isMod bool) string {
	if isMod {
		return " (a mod map)"
	}
	return ""
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: v,
	}, nil
}
