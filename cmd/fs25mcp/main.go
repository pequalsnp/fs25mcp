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
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pequalsnp/fs25mcp/internal/fs25data"
	"github.com/pequalsnp/fs25mcp/internal/fs25save"
	"github.com/pequalsnp/fs25mcp/internal/locate"
)

const usage = `fs25mcp — Farming Simulator 25 over MCP

  fs25mcp                 serve over stdio (auto-detects game and savegame)
  fs25mcp status          print what was found, and exit
  fs25mcp play -- CMD     launch the game with the server alongside it,
                          and stop when the game does. On Steam put
                          "fs25mcp play --relay ws://host:8091/relay/fs25 -- %command%"
                          in Launch Options and never think about it again.
  fs25mcp -h              flags

Nothing needs configuring. Point -install/-save at them only if the
auto-detection misses, which it will on a Steam library that lives on
another drive.
`

func main() {
	// Transport flags come out first, by the convention shared with
	// x4mcp (cli.go): recognised before OR after the subcommand, and
	// never scanned past "--", which is where the game's own command
	// line begins. Go's flag package cannot do either, and the second
	// one matters: it used to swallow "-relay" as a game argument and
	// launch the game with no server attached, silently.
	tr, args, err := parseTransport(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "fs25mcp: %v\n%s", err, transportUsage)
		os.Exit(2)
	}

	fs := flag.NewFlagSet("fs25mcp", flag.ExitOnError)
	installDir := fs.String("install", "", "game install directory (default: auto-detect)")
	saveDir := fs.String("save", "", "savegame directory (default: auto-detect)")
	savegame := fs.String("savegame", "", "which savegame, e.g. savegame4 (default: most recently played)")
	fs.Usage = func() {
		_, _ = os.Stderr.WriteString(usage)
		fs.PrintDefaults()
		_, _ = os.Stderr.WriteString(transportUsage)
	}
	// Only the arguments up to "--" are ours; everything after belongs to
	// the game and is handed back untouched.
	own, gameCmd := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			own, gameCmd = args[:i], args[i:]
			break
		}
	}
	// The subcommand, if any, leads. Strip it so the remaining flags parse.
	sub := ""
	if len(own) > 0 && !strings.HasPrefix(own[0], "-") {
		sub, own = own[0], own[1:]
	}
	if err := fs.Parse(own); err != nil {
		os.Exit(2)
	}

	src := &source{installDir: *installDir, saveDir: *saveDir, savegame: *savegame}

	// "serve" is accepted and means the default, so the two tools take the
	// same command line. An unrecognised subcommand is an error rather
	// than a silent fall-through to serving: a typo'd "statsu" that
	// quietly started a server instead of printing status is exactly the
	// kind of thing nobody notices until the answers are stale.
	switch sub {
	case "", "serve", "status", "play":
	default:
		fmt.Fprintf(os.Stderr, "fs25mcp: unknown subcommand %q (want: serve|status|play)\n%s", sub, usage)
		os.Exit(2)
	}

	if sub == "status" {
		if err := src.ensure(); err != nil {
			fmt.Fprintf(os.Stderr, "fs25mcp: %v\n", err)
			os.Exit(1)
		}
		src.printStatus()
		return
	}

	if sub == "play" {
		if err := src.ensure(); err != nil {
			// Not fatal: the game is about to be launched, and on a first
			// run the savegame does not exist until it has been played.
			log.Printf("fs25mcp: %v (will retry once the game has saved)", err)
		}
		if err := runPlay(context.Background(), src, gameCmd, tr.http, tr.relay); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Deliberately NOT fatal when the game is missing. This runs as a
	// companion service that starts at login, which is before Steam has
	// mounted a library on another drive, before a fresh install has any
	// savegame, and while the player may simply not have got round to
	// playing yet. Exiting would put systemd into a ten-second restart
	// loop for as long as that lasts; instead it serves, and every tool
	// reports the problem in words the player can act on.
	if err := src.ensure(); err != nil {
		log.Printf("fs25mcp: %v (will retry when a tool is called)", err)
	}

	server := newServer(src)
	ctx := context.Background()

	// stdio: the client launches this process and owns its lifetime, which
	// is how test harnesses and desktop MCP clients prefer to work — no
	// port to collide with, no server left running afterwards.
	//
	// Logging MUST go to stderr here and nowhere near stdout: stdout is
	// the JSON-RPC channel, and one stray line of log corrupts the stream.
	// log's default is already stderr; this is a note against changing it.
	if tr.relay != "" {
		log.Printf("fs25mcp: dialing relay %s", tr.relay)
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
		log.Fatal(serveConnect(ctx, handler, tr.relay))
	}

	if tr.http != "" {
		log.Printf("fs25mcp: serving on http://%s", tr.http)
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
		srv := &http.Server{Addr: tr.http, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(srv.ListenAndServe())
	}

	// stdio is the default, matching x4mcp and the MCP convention: the
	// client launches the server and owns the pipes. Logging stays on
	// stderr — stdout is the JSON-RPC channel and one stray line corrupts
	// it.
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("fs25mcp: stdio: %v", err)
	}
}

// source is everything the tools read: the install, and one savegame.
// Resolution is lazy so the service can start before the game exists.
type source struct {
	// Overrides, empty meaning auto-detect.
	installDir string
	saveDir    string
	savegame   string

	Install     *fs25data.Install
	Save        *fs25save.Save
	SaveName    string
	SaveDir     string
	InstallPath string
}

// ensure resolves the install and savegame if that has not happened yet.
// Repeated calls after success are free; after failure it tries again,
// which is what makes "start the service, then install the game" work.
func (s *source) ensure() error {
	if s.Install != nil && s.Save != nil {
		return nil
	}
	got, err := resolve(s.installDir, s.saveDir, s.savegame)
	if err != nil {
		return err
	}
	s.Install, s.Save = got.Install, got.Save
	s.SaveName, s.SaveDir, s.InstallPath = got.SaveName, got.SaveDir, got.InstallPath
	return nil
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
	if err := s.ensure(); err != nil {
		return err
	}
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
