// Package locate finds the game and the savegames without being told.
//
// This is the difference between a tool a gamer runs and a tool a gamer
// reads the README of. Nobody knows the path to their Proton prefix, and
// nobody should have to: the install and the saves live in a handful of
// predictable places on every platform the game ships for, and the most
// recently modified savegame is almost always the one being played.
//
// Everything here degrades to "tell me with a flag" rather than failing.
package locate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// steamAppID is Farming Simulator 25 on Steam. It names the Proton
// prefix, which is where the Windows-side save directory lives on Linux.
const steamAppID = "2300320"

// installNames are the folder names the game ships under. The Steam and
// GIANTS/Epic installers disagree, so both are tried.
var installNames = []string{
	"Farming Simulator 25",
	"FarmingSimulator2025",
}

// Install returns the game installation directory.
func Install() (string, error) {
	for _, dir := range installCandidates() {
		// data/maps is the cheapest proof this is really the game and not
		// an empty folder Steam left behind after an uninstall.
		if isDir(filepath.Join(dir, "data", "maps")) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no Farming Simulator 25 install found; pass -install")
}

// SaveDir returns the directory holding savegame1..N.
func SaveDir() (string, error) {
	for _, dir := range saveCandidates() {
		if isDir(dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no Farming Simulator 25 save directory found; pass -save")
}

// Savegame is one savegame directory and when it was last written.
type Savegame struct {
	Dir      string
	Name     string // "savegame1"
	Modified time.Time
}

// Savegames lists savegames newest first. A slot the player created and
// never used is skipped: the game leaves a directory with nothing but a
// stub in it, and offering those as choices is noise.
func Savegames(saveDir string) ([]Savegame, error) {
	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return nil, err
	}
	var out []Savegame
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "savegame") {
			continue
		}
		dir := filepath.Join(saveDir, e.Name())
		career := filepath.Join(dir, "careerSavegame.xml")
		fi, err := os.Stat(career)
		if err != nil {
			continue
		}
		out = append(out, Savegame{Dir: dir, Name: e.Name(), Modified: fi.ModTime()})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no savegames in %s", saveDir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// Latest returns the most recently written savegame — the one being
// played, in every case that matters.
func Latest(saveDir string) (Savegame, error) {
	saves, err := Savegames(saveDir)
	if err != nil {
		return Savegame{}, err
	}
	return saves[0], nil
}

func installCandidates() []string {
	var out []string
	add := func(base string) {
		for _, n := range installNames {
			out = append(out, filepath.Join(base, n))
		}
	}
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		for _, drive := range []string{`C:\`, `D:\`, `E:\`} {
			add(filepath.Join(drive, "Program Files (x86)", "Steam", "steamapps", "common"))
			add(filepath.Join(drive, "Program Files", "Farming Simulator 2025"))
			add(filepath.Join(drive, "SteamLibrary", "steamapps", "common"))
		}
	default:
		// Steam's default library, plus the two layouts Flatpak and older
		// installs use. Extra library folders on other drives are why
		// -install exists.
		add(filepath.Join(home, ".local", "share", "Steam", "steamapps", "common"))
		add(filepath.Join(home, ".steam", "steam", "steamapps", "common"))
		add(filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam", "steamapps", "common"))
		for _, drive := range []string{"/mnt", "/media", "/run/media"} {
			add(filepath.Join(drive, "games", "SteamLibrary", "steamapps", "common"))
		}
	}
	return out
}

func saveCandidates() []string {
	var out []string
	home, _ := os.UserHomeDir()

	if runtime.GOOS == "windows" {
		out = append(out, filepath.Join(home, "Documents", "My Games", "FarmingSimulator2025"))
		return out
	}

	// On Linux the game runs under Proton, so the save lives inside the
	// prefix at the Windows path — which is why "it's in Documents" is
	// useless advice here.
	prefixes := []string{
		filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata"),
		filepath.Join(home, ".steam", "steam", "steamapps", "compatdata"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam", "steamapps", "compatdata"),
	}
	for _, p := range prefixes {
		out = append(out,
			filepath.Join(p, steamAppID, "pfx", "drive_c", "users", "steamuser", "Documents", "My Games", "FarmingSimulator2025"))
	}
	// A native/Wine install that keeps saves in the real home.
	out = append(out, filepath.Join(home, "Documents", "My Games", "FarmingSimulator2025"))
	return out
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
