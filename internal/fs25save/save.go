// Package fs25save reads a Farming Simulator 25 savegame directory.
//
// The save is plain XML, unlike X4's single compressed blob, so this is a
// handful of small readers rather than a streaming parser. What it
// deliberately does NOT read is the <players> block in farms.xml: that
// carries uniqueUserId and lastNickname, which are personal identifiers
// with no bearing on any farming question. A public tool that answers
// "how is my farm doing" has no business handling them.
package fs25save

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Save is the state of one savegame, as much as answering planning
// questions needs.
type Save struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Map  string `json:"map"`
	// MapID is the raw id. A map shipped as a mod carries the mod's zip
	// name here (FS25_Gnadenthal_crossplay.Gnadenthal), which is the only
	// reliable signal available — see MapIsMod.
	MapID string `json:"map_id"`
	// MapIsMod is a HEURISTIC: mod maps are addressed through their zip,
	// so the id carries an FS25_ prefix where a base map does not. Treat
	// it as "probably", and say so rather than asserting.
	MapIsMod           bool    `json:"map_is_mod"`
	PlayTimeHours      float64 `json:"play_time_hours"`
	SaveDate           string  `json:"save_date"`
	EconomicDifficulty string  `json:"economic_difficulty"`

	FarmName string  `json:"farm_name"`
	Money    float64 `json:"money"`
	Loan     float64 `json:"loan"`

	FarmlandOwned int `json:"farmland_owned"`
	FarmlandTotal int `json:"farmland_total"`
	Vehicles      int `json:"vehicles"`

	Productions []Production `json:"productions"`
}

// Production is one production point the player owns, with the recipes
// actually switched on and what is sitting in its storage.
type Production struct {
	Kind string `json:"kind"`
	// Mod is the mod this building came from, empty for base game. The
	// save records it directly (modName, and a $moddir$ filename), which
	// makes this the ONE place provenance is a fact rather than an
	// inference — the store documents cannot tell you, because mod
	// content never appears in them.
	Mod string `json:"mod,omitempty"`
	// Source is "base game" or "mod: <name>", precomputed so a caller
	// cannot forget to say which.
	Source   string             `json:"source"`
	Price    float64            `json:"price"`
	Enabled  []string           `json:"enabled_recipes"`
	Disabled []string           `json:"disabled_recipes"`
	Storage  map[string]float64 `json:"storage,omitempty"`
}

// Read parses a savegame directory. Missing optional files degrade to
// zero values rather than failing: a save mid-write, or one from a
// version that dropped a file, should still answer what it can.
func Read(dir string) (*Save, error) {
	s := &Save{Dir: dir}
	if err := s.readCareer(filepath.Join(dir, "careerSavegame.xml")); err != nil {
		return nil, err
	}
	// Everything below is best-effort by design.
	_ = s.readFarms(filepath.Join(dir, "farms.xml"))
	_ = s.readFarmland(filepath.Join(dir, "farmland.xml"))
	_ = s.readVehicles(filepath.Join(dir, "vehicles.xml"))
	_ = s.readPlaceables(filepath.Join(dir, "placeables.xml"))
	return s, nil
}

type careerXML struct {
	SavegameName       string  `xml:"settings>savegameName"`
	MapTitle           string  `xml:"settings>mapTitle"`
	MapID              string  `xml:"settings>mapId"`
	SaveDate           string  `xml:"settings>saveDate"`
	PlayTime           float64 `xml:"statistics>playTime"`
	EconomicDifficulty string  `xml:"settings>economicDifficulty"`
}

func (s *Save) readCareer(path string) error {
	var c careerXML
	if err := decode(path, &c); err != nil {
		return fmt.Errorf("read careerSavegame: %w", err)
	}
	s.Name = c.SavegameName
	s.Map = c.MapTitle
	s.MapID = c.MapID
	s.SaveDate = c.SaveDate
	s.PlayTimeHours = c.PlayTime
	s.EconomicDifficulty = c.EconomicDifficulty
	s.MapIsMod = strings.HasPrefix(c.MapID, "FS25_")
	return nil
}

type farmsXML struct {
	Farms []struct {
		FarmID string  `xml:"farmId,attr"`
		Name   string  `xml:"name,attr"`
		Money  float64 `xml:"money,attr"`
		Loan   float64 `xml:"loan,attr"`
	} `xml:"farm"`
}

// readFarms takes farm 1, the human player's farm. Farm 0 is the
// unowned/AI bucket and never has a balance worth reporting.
func (s *Save) readFarms(path string) error {
	var f farmsXML
	if err := decode(path, &f); err != nil {
		return err
	}
	for _, farm := range f.Farms {
		if farm.FarmID == "1" {
			s.FarmName = farm.Name
			s.Money = farm.Money
			s.Loan = farm.Loan
			return nil
		}
	}
	return nil
}

type farmlandXML struct {
	Farmlands []struct {
		FarmID string `xml:"farmId,attr"`
	} `xml:"farmland"`
}

func (s *Save) readFarmland(path string) error {
	var f farmlandXML
	if err := decode(path, &f); err != nil {
		return err
	}
	s.FarmlandTotal = len(f.Farmlands)
	for _, fl := range f.Farmlands {
		if fl.FarmID != "" && fl.FarmID != "0" {
			s.FarmlandOwned++
		}
	}
	return nil
}

type vehiclesXML struct {
	Vehicles []struct{} `xml:"vehicle"`
}

func (s *Save) readVehicles(path string) error {
	var v vehiclesXML
	if err := decode(path, &v); err != nil {
		return err
	}
	s.Vehicles = len(v.Vehicles)
	return nil
}

type placeablesXML struct {
	Placeables []struct {
		UniqueID   string  `xml:"uniqueId,attr"`
		ModName    string  `xml:"modName,attr"`
		Filename   string  `xml:"filename,attr"`
		Price      float64 `xml:"price,attr"`
		FarmID     string  `xml:"farmId,attr"`
		Production *struct {
			Productions []struct {
				ID      string `xml:"id,attr"`
				Enabled string `xml:"isEnabled,attr"`
			} `xml:"production"`
			Storage struct {
				Nodes []struct {
					FillType string  `xml:"fillType,attr"`
					Level    float64 `xml:"fillLevel,attr"`
				} `xml:"node"`
			} `xml:"storage"`
		} `xml:"productionPoint"`
	} `xml:"placeable"`
}

func (s *Save) readPlaceables(path string) error {
	var p placeablesXML
	if err := decode(path, &p); err != nil {
		return err
	}
	for _, pl := range p.Placeables {
		if pl.Production == nil || (pl.FarmID != "1" && pl.FarmID != "") {
			continue
		}
		prod := Production{
			Kind:   kindOf(pl.UniqueID, pl.Filename),
			Mod:    pl.ModName,
			Source: sourceOf(pl.ModName),
			Price:  pl.Price,
		}
		for _, r := range pl.Production.Productions {
			if r.Enabled == "true" {
				prod.Enabled = append(prod.Enabled, r.ID)
			} else {
				prod.Disabled = append(prod.Disabled, r.ID)
			}
		}
		for _, n := range pl.Production.Storage.Nodes {
			if n.Level <= 0 {
				continue
			}
			if prod.Storage == nil {
				prod.Storage = map[string]float64{}
			}
			prod.Storage[n.FillType] += n.Level
		}
		s.Productions = append(s.Productions, prod)
	}
	sort.SliceStable(s.Productions, func(i, j int) bool {
		return s.Productions[i].Kind < s.Productions[j].Kind
	})
	return nil
}

// kindOf names the building. Preplaced ids carry the type in the middle
// segment ("preplaced_grainFlourMillUS_<hash>"), but anything the player
// built themselves is just "placeable<hash>" — for those the filename is
// the only readable source ("$moddir$FS25_x/solarStation.xml").
func kindOf(uniqueID, filename string) string {
	if strings.HasPrefix(uniqueID, "preplaced_") {
		if parts := strings.Split(uniqueID, "_"); len(parts) >= 2 {
			return parts[1]
		}
	}
	if filename != "" {
		base := filepath.Base(filename)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return uniqueID
}

// SourceBaseGame labels content that came with the game.
const SourceBaseGame = "base game"

func sourceOf(modName string) string {
	if modName == "" {
		return SourceBaseGame
	}
	return "mod: " + modName
}

func decode(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Saves are written with a UTF-8 BOM, which encoding/xml rejects.
	b = trimBOM(b)
	return xml.Unmarshal(b, v)
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
