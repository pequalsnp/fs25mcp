// Package gamedata parses a local Farming Simulator 25 installation into
// the facts a planning expert actually needs: what the shop sells, what
// the crops yield, what the goods are worth, what the factories turn into
// what, and which DLC is installed.
//
// Why parse the install at all: the game ships its own economy. Prices,
// yields, seed rates, engine power, working widths and production recipes
// all live in plain XML under `data/`, versioned by the build in
// `VERSION`. That is authoritative in a way no wiki is — a wiki lags
// patches, mixes FS22 numbers in, and disagrees with itself. Reading the
// install is also free, offline, and exactly as current as the machine
// the player is on.
//
// Why aggregation is the whole design: `data/` holds ~4,200 XML files,
// 606 of which are store items. Emitting a document per file would put
// several thousand near-identical stubs into the same vector space as
// years of forum threads, and retrieval would return "here is one
// tractor" for every question. So this package groups instead: store
// items collapse into a handful of purpose groups (tractors, harvesting,
// tillage, ...), crops into one table, fill types into one price table,
// every factory recipe into one chain document. Tens of dense documents,
// each a list whose every line stands on its own, because a chunk that
// gets split away from its header still has to answer a question.
//
// What cannot be read, and why:
//
//   - DLC content. `pdlc/*.dlc` and `dataS*.gar` are encrypted GIANTS
//     archives (the "GAR " magic is there, the payload is not plaintext).
//     So installed DLC is reported as presence and pack name only; the
//     vehicles inside them are invisible here and the documents say so
//     rather than implying the base-game list is complete.
//   - Translations. Display strings resolve through `$l10n_*` keys whose
//     values live inside those same archives. Most vehicle names are
//     literal (569 of 606), and the rest — plus every fill type title and
//     function label — are recovered by humanising the key tail
//     ("$l10n_fillType_riceLongGrain" -> "Rice Long Grain"). That is
//     close enough to search text and never wrong about identity, since
//     the raw key name is carried alongside.
//
// Two conventions inherited from the game data are worth stating: price
// periods are 1-12 starting at *March*, not January (the game's year
// starts with the spring planting season), and yields are given per
// square metre, which this package also renders per hectare because
// that is the unit a player plans fields in.
//
// The package is a parser and nothing else: filesystem in, structs out,
// no database, no network. Callers turn Documents into whatever their
// ingest layer wants.
package fs25data

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Install is one parsed Farming Simulator 25 installation.
type Install struct {
	Root    string // absolute or caller-supplied path to the install
	Version string // contents of VERSION, e.g. "1.21.1.0"
	// VersionTime is when this build landed on this machine (the mtime of
	// VERSION). It exists so callers have a publication date that only
	// moves when the game does: dating these documents "now" would move
	// the payload hash on every run and re-chunk a corpus that did not
	// change.
	VersionTime time.Time
	StoreItems  []StoreItem
	Crops       []Crop
	FillTypes   []FillType
	Factories   []Factory
	DLCs        []DLC

	// Skipped counts XML files that looked like content but failed to
	// parse. A non-zero value is a hint that the install is damaged or
	// the schema moved; it is never fatal, because one bad tractor must
	// not cost the operator the other 605.
	Skipped int
}

// StoreItem is one purchasable vehicle, tool or implement.
type StoreItem struct {
	File       string // path relative to the install root
	Name       string
	Brand      string
	Type       string   // vehicle type attribute, e.g. "tractor"
	Categories []string // store categories; the game allows several
	Group      string   // derived purpose group, see Groups
	Function   string   // humanised first store function line

	Price        int
	Lifetime     int
	Power        float64 // hp, self-propelled only
	NeededPower  float64 // hp required to pull it
	MaxSpeed     float64 // km/h
	WorkingWidth float64 // m
	Weight       float64 // kg, summed over physics components

	// Capacity is the payload: the largest fill unit of the default
	// configuration that holds something the vehicle is bought to move.
	Capacity      float64
	CapacityFills string
	// TankCapacity is the service fluid the vehicle carries to run
	// itself — a tractor's fuel tank, and also the whole payload of a
	// fuel bowser. Kept apart from Capacity so neither invents the other.
	TankCapacity float64
	TankFills    string
}

// Crop is one fruit type: what it costs to sow and what it returns.
type Crop struct {
	File     string
	Name     string // raw fruit type name, e.g. "riceLongGrain"
	Title    string // humanised, e.g. "Rice Long Grain"
	OnMap    bool
	Missions bool // usable for field missions
	// Sowable and Cultivate are only meaningful when the matching
	// *Declared flag is set. A missing <seeding>/<cultivation> element
	// means the game did not say, and "did not say" must never be
	// rendered as "no" — a document asserting that wheat cannot be sown
	// is worse than a document that stays quiet about it.
	Sowable           bool
	SowableDeclared   bool
	Cultivate         bool
	CultivateDeclared bool

	SeedPerSqm    float64
	YieldPerSqm   float64
	CutHeight     float64
	WindrowFill   string // straw/grass left behind on harvest
	WindrowPerSqm float64

	NeedsRolling bool
	RequiresLime bool
	ConsumesLime bool
	NeedsLowSoil bool
	// Headers and Planters both come from the game's fruitTypeCategories,
	// which mixes "what harvests this" and "what sows this" in one list.
	// Conflating them would tell a player a sowing machine harvests wheat.
	Headers       []string
	Planters      []string
	Conversions   []Conversion
	CutFillType   string
	ChopperOutput string
}

// Conversion is a machine turning one crop or fill type into another.
type Conversion struct {
	Machine string // converter name, e.g. "FORAGEHARVESTER"
	To      string
	Factor  float64
}

// FillType is one sellable or storable good plus its economy.
type FillType struct {
	Name          string // raw name, e.g. "RICELONGGRAIN"
	Title         string // humanised title
	PricePerLiter float64
	MassPerLiter  float64
	OnPriceTable  bool
	Bulk          bool
	Pallet        bool
	Bale          bool
	// Livestock fill types are priced and counted per animal even though
	// the game stores them in the same litre-denominated table as wheat.
	// "A cow costs 5000 per litre" is the kind of sentence a knowledge
	// base must never emit.
	Livestock bool
	// Factors are the 12 seasonal price multipliers in game order:
	// index 0 is March, index 11 is February.
	Factors    []float64
	Categories []string
}

// Factory is a production point and the recipes it runs.
type Factory struct {
	File    string
	Name    string
	Price   int
	Recipes []Recipe
}

// Recipe is one production line: inputs consumed per cycle, outputs made.
type Recipe struct {
	ID       string
	Inputs   []Ingredient
	Outputs  []Ingredient
	PerHour  float64 // cycles per hour at full throughput
	CostHour float64 // running cost per active hour
}

// Ingredient is one side of a recipe.
type Ingredient struct {
	FillType string
	Title    string
	Amount   float64
}

// DLC is one installed downloadable content pack. Contents are encrypted,
// so presence and name are all this reports.
type DLC struct {
	File  string
	Name  string // pack id, e.g. "macDonPack"
	Title string // humanised, e.g. "Mac Don Pack"
	Size  int64
}

// monthNames indexes the game's price periods, which start in March.
var monthNames = []string{"March", "April", "May", "June", "July", "August",
	"September", "October", "November", "December", "January", "February"}

// Parse reads an installation directory. Individual unreadable or
// unexpected XML files are skipped and counted rather than failing the
// run: a game install is a third-party artifact that changes every patch,
// and an operator would rather have 605 tractors than an error.
func Parse(root string) (*Install, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("parse install: root path is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat install root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("parse install: %s is not a directory", root)
	}
	dataDir := filepath.Join(root, "data")
	if di, err := os.Stat(dataDir); err != nil || !di.IsDir() {
		return nil, fmt.Errorf("parse install: %s has no data/ directory (not a Farming Simulator install?)", root)
	}

	in := &Install{Root: root}
	in.Version, in.VersionTime = readVersion(root)
	in.parseStoreItems()
	in.parseCrops()
	in.parseFillTypes()
	in.parseFactories()
	in.parseDLCs()
	return in, nil
}

func readVersion(root string) (version string, modified time.Time) {
	path := filepath.Join(root, "VERSION")
	b, err := os.ReadFile(path)
	if err != nil {
		slog.Debug("gamedata: no VERSION file", "root", root, "error", err)
		return "", time.Time{}
	}
	if fi, statErr := os.Stat(path); statErr == nil {
		modified = fi.ModTime().UTC()
	}
	return strings.TrimSpace(string(b)), modified
}

// decodeXML unmarshals a game XML file. The game writes a UTF-8 BOM ahead
// of the declaration on most files, which encoding/xml rejects outright,
// and declares charsets the stdlib does not carry a decoder for; both are
// handled here so every call site stays a plain struct unmarshal.
func decodeXML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read xml: %w", err)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode xml %s: %w", filepath.Base(path), err)
	}
	return nil
}

// ---------------------------------------------------------------- store

type rawComponent struct {
	Mass float64 `xml:"mass,attr"`
}

type rawFillUnit struct {
	Capacity   float64 `xml:"capacity,attr"`
	FillTypes  string  `xml:"fillTypes,attr"`
	Categories string  `xml:"fillTypeCategories,attr"`
}

// rawName is a store name: a printf-style template plus the parameters
// the game substitutes into it. John Deere's tractors are all named
// "$l10n_shopItem_seriesX" and differ only by params ("8R", "9000"), so
// the attribute is the identity, not decoration.
type rawName struct {
	Text   string `xml:",chardata"`
	Params string `xml:"params,attr"`
}

type rawVehicle struct {
	Type      string `xml:"type,attr"`
	StoreData struct {
		Name        rawName  `xml:"name"`
		Brand       string   `xml:"brand"`
		Category    string   `xml:"category"`
		Price       int      `xml:"price"`
		Lifetime    int      `xml:"lifetime"`
		ShowInStore *bool    `xml:"showInStore"`
		Functions   []string `xml:"functions>function"`
		Specs       struct {
			Power        float64 `xml:"power"`
			NeededPower  float64 `xml:"neededPower"`
			MaxSpeed     float64 `xml:"maxSpeed"`
			WorkingWidth float64 `xml:"workingWidth"`
		} `xml:"specs"`
	} `xml:"storeData"`
	Base struct {
		Components []rawComponent `xml:"components>component"`
		Configs    []struct {
			Components []rawComponent `xml:"component"`
		} `xml:"componentConfigurations>componentConfiguration"`
	} `xml:"base"`
	// Every fill unit in 1.21 sits under a configuration, because the
	// shop offers tank-size options. The unwrapped shape is declared too
	// so a future patch that drops the configuration layer degrades to
	// "capacity still parsed" rather than "capacity silently zero".
	FillUnitConfigs []struct {
		Units []rawFillUnit `xml:"fillUnits>fillUnit"`
	} `xml:"fillUnit>fillUnitConfigurations>fillUnitConfiguration"`
	PlainFillUnits []rawFillUnit `xml:"fillUnit>fillUnits>fillUnit"`
}

// defaultFillUnits returns the units the shop quotes: the first
// configuration if the vehicle has configurations, else the bare list.
func (raw rawVehicle) defaultFillUnits() []rawFillUnit {
	if len(raw.FillUnitConfigs) > 0 {
		return raw.FillUnitConfigs[0].Units
	}
	return raw.PlainFillUnits
}

func (in *Install) parseStoreItems() {
	dir := filepath.Join(in.Root, "data", "vehicles")
	in.walkXML(dir, func(path, rel string) {
		var raw rawVehicle
		if err := decodeXML(path, &raw); err != nil {
			slog.Debug("gamedata: skipped vehicle", "file", rel, "error", err)
			in.Skipped++
			return
		}
		sd := raw.StoreData
		if strings.TrimSpace(sd.Name.Text) == "" && strings.TrimSpace(sd.Category) == "" {
			return // a shared/config fragment, not a store item
		}
		if sd.ShowInStore != nil && !*sd.ShowInStore {
			return // present in the data, never purchasable
		}

		item := StoreItem{
			File:         rel,
			Name:         storeName(sd.Name),
			Brand:        brandName(sd.Brand),
			Type:         raw.Type,
			Categories:   strings.Fields(sd.Category),
			Price:        sd.Price,
			Lifetime:     sd.Lifetime,
			Power:        sd.Specs.Power,
			NeededPower:  sd.Specs.NeededPower,
			MaxSpeed:     sd.Specs.MaxSpeed,
			WorkingWidth: sd.Specs.WorkingWidth,
			Weight:       componentMass(raw),
		}
		if len(sd.Functions) > 0 {
			item.Function = humanize(sd.Functions[0])
		}
		if item.Name == "" {
			item.Name = prettify(strings.TrimSuffix(filepath.Base(rel), ".xml"))
		}
		item.Group = GroupFor(item.Categories)
		// Payload and service tank are tracked apart. The largest unit on
		// a tractor is its 4000-litre pneumatic brake reservoir, which is
		// not a fact anyone wants; the largest unit on a Thunder Creek
		// FST 990 is 3750 litres of diesel, which is the entire point of
		// the vehicle. Lumping them together loses one or invents the
		// other.
		for _, u := range raw.defaultFillUnits() {
			fills := strings.ToLower(strings.TrimSpace(u.FillTypes + " " + u.Categories))
			switch {
			case isAirFill(fills):
				// Brake reservoir. Never a payload, never a tank worth
				// reporting.
			case isServiceFill(fills):
				if u.Capacity > item.TankCapacity {
					item.TankCapacity = u.Capacity
					item.TankFills = fills
				}
			case u.Capacity > item.Capacity:
				item.Capacity = u.Capacity
				item.CapacityFills = fills
			}
		}
		// Store-pack umbrella entries (an "8010 Series" tile that opens a
		// submenu) carry a name and a category and nothing else. They
		// state no fact, so they are not documents.
		if item.Price == 0 && item.Power == 0 && item.NeededPower == 0 &&
			item.WorkingWidth == 0 && item.Capacity == 0 && item.TankCapacity == 0 &&
			item.Weight == 0 {
			return
		}
		in.StoreItems = append(in.StoreItems, item)
	})

	sort.Slice(in.StoreItems, func(i, j int) bool {
		a, b := in.StoreItems[i], in.StoreItems[j]
		if a.Brand != b.Brand {
			return a.Brand < b.Brand
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.File < b.File
	})
}

// serviceFills are the fluids a vehicle carries to run itself rather than
// to do work — a fuel tank, not a payload. A vehicle whose entire job is
// carrying one of them still reports it, as a tank rather than a payload.
var serviceFills = map[string]bool{
	"diesel": true, "def": true, "electriccharge": true,
	"methane": true, "silage_additive": true,
}

func isServiceFill(fills string) bool {
	names := strings.Fields(strings.ToLower(fills))
	if len(names) == 0 {
		return false
	}
	for _, n := range names {
		if !serviceFills[n] {
			return false
		}
	}
	return true
}

// isAirFill spots the pneumatic brake reservoir, which is the largest
// fill unit on most tractors and means nothing to anybody.
func isAirFill(fills string) bool {
	names := strings.Fields(strings.ToLower(fills))
	if len(names) == 0 {
		return false
	}
	for _, n := range names {
		if n != "air" {
			return false
		}
	}
	return true
}

func componentMass(raw rawVehicle) float64 {
	var total float64
	for _, c := range raw.Base.Components {
		total += c.Mass
	}
	if total > 0 {
		return total
	}
	// Configurable chassis carry their mass on the first configuration.
	if len(raw.Base.Configs) > 0 {
		for _, c := range raw.Base.Configs[0].Components {
			total += c.Mass
		}
	}
	return total
}

// ---------------------------------------------------------------- crops

type rawFruitTypes struct {
	Types []struct {
		Filename string `xml:"filename,attr"`
	} `xml:"fruitTypes>fruitType"`
	Categories []struct {
		Name   string `xml:"name,attr"`
		Fruits string `xml:",chardata"`
	} `xml:"fruitTypeCategories>fruitTypeCategory"`
	Converters []struct {
		Name       string `xml:"name,attr"`
		Converters []struct {
			From   string  `xml:"from,attr"`
			To     string  `xml:"to,attr"`
			Factor float64 `xml:"factor,attr"`
		} `xml:"converter"`
	} `xml:"fruitTypeConverters>fruitTypeConverter"`
}

type rawFoliage struct {
	FruitType struct {
		Name     string `xml:"name,attr"`
		OnMap    bool   `xml:"shownOnMap,attr"`
		Missions bool   `xml:"useForFieldMissions,attr"`
		Windrow  *struct {
			FillType     string  `xml:"fillType,attr"`
			LitersPerSqm float64 `xml:"litersPerSqm,attr"`
			CutFillType  string  `xml:"cutFillType,attr"`
		} `xml:"windrow"`
		Harvest *struct {
			LitersPerSqm float64 `xml:"litersPerSqm,attr"`
			CutHeight    float64 `xml:"cutHeight,attr"`
			ChopperType  string  `xml:"chopperType,attr"`
		} `xml:"harvest"`
		Growth *struct {
			RequiresLime bool `xml:"growthRequiresLime,attr"`
		} `xml:"growth"`
		Soil *struct {
			LowDensityRequired bool `xml:"lowDensityRequired,attr"`
			ConsumesLime       bool `xml:"consumesLime,attr"`
		} `xml:"soil"`
		Seeding *struct {
			LitersPerSqm float64 `xml:"litersPerSqm,attr"`
			NeedsRolling bool    `xml:"needsRolling,attr"`
			IsAvailable  bool    `xml:"isAvailable,attr"`
		} `xml:"seeding"`
		Cultivation *struct {
			IsAllowed bool `xml:"isAllowed,attr"`
		} `xml:"cultivation"`
	} `xml:"fruitType"`
}

func (in *Install) parseCrops() {
	index := filepath.Join(in.Root, "data", "maps", "maps_fruitTypes.xml")
	var raw rawFruitTypes
	if err := decodeXML(index, &raw); err != nil {
		slog.Debug("gamedata: no fruit type index", "error", err)
		return
	}

	// Keys are upper-cased on insert as well as on lookup. The game
	// writes these tokens in caps today, but a lookup that assumes the
	// file's casing fails silently — every crop would simply lose its
	// equipment lines and the parse would still "succeed".
	headers, planters := map[string][]string{}, map[string][]string{}
	for _, c := range raw.Categories {
		target := headers
		if isPlantingCategory(c.Name) {
			target = planters
		}
		for _, fruit := range strings.Fields(c.Fruits) {
			key := strings.ToUpper(fruit)
			target[key] = append(target[key], c.Name)
		}
	}
	conversions := map[string][]Conversion{}
	for _, group := range raw.Converters {
		for _, c := range group.Converters {
			key := strings.ToUpper(c.From)
			conversions[key] = append(conversions[key],
				Conversion{Machine: group.Name, To: c.To, Factor: c.Factor})
		}
	}

	for _, ref := range raw.Types {
		path, rel, ok := in.resolve(ref.Filename)
		if !ok {
			continue
		}
		var f rawFoliage
		if err := decodeXML(path, &f); err != nil {
			slog.Debug("gamedata: skipped fruit type", "file", rel, "error", err)
			in.Skipped++
			continue
		}
		ft := f.FruitType
		if ft.Name == "" {
			continue
		}
		key := strings.ToUpper(ft.Name)
		crop := Crop{
			File:        rel,
			Name:        ft.Name,
			Title:       prettify(ft.Name),
			OnMap:       ft.OnMap,
			Missions:    ft.Missions,
			Headers:     headers[key],
			Planters:    planters[key],
			Conversions: conversions[key],
		}
		if w := ft.Windrow; w != nil {
			crop.WindrowFill = w.FillType
			crop.WindrowPerSqm = w.LitersPerSqm
			crop.CutFillType = w.CutFillType
		}
		if h := ft.Harvest; h != nil {
			crop.YieldPerSqm = h.LitersPerSqm
			crop.CutHeight = h.CutHeight
			crop.ChopperOutput = h.ChopperType
		}
		if g := ft.Growth; g != nil {
			crop.RequiresLime = g.RequiresLime
		}
		if s := ft.Soil; s != nil {
			crop.NeedsLowSoil = s.LowDensityRequired
			crop.ConsumesLime = s.ConsumesLime
		}
		if s := ft.Seeding; s != nil {
			crop.SeedPerSqm = s.LitersPerSqm
			crop.NeedsRolling = s.NeedsRolling
			crop.Sowable, crop.SowableDeclared = s.IsAvailable, true
		}
		if c := ft.Cultivation; c != nil {
			crop.Cultivate, crop.CultivateDeclared = c.IsAllowed, true
		}
		in.Crops = append(in.Crops, crop)
	}
}

// isPlantingCategory splits the game's one fruitTypeCategories list into
// the half that puts a crop in the ground and the half that takes it out.
func isPlantingCategory(name string) bool {
	n := strings.ToUpper(name)
	return strings.Contains(n, "SOWING") || strings.Contains(n, "PLANTER")
}

// resolve turns a `$data/...` game path into a filesystem path inside the
// install. `$dataS/` and other archive-relative roots resolve to nothing:
// those live inside the encrypted GAR files.
func (in *Install) resolve(gamePath string) (path, rel string, ok bool) {
	p := strings.TrimSpace(gamePath)
	if !strings.HasPrefix(p, "$data/") {
		return "", "", false
	}
	rel = filepath.FromSlash("data/" + strings.TrimPrefix(p, "$data/"))
	path = filepath.Join(in.Root, rel)
	if _, err := os.Stat(path); err != nil {
		return "", "", false
	}
	return path, rel, true
}

// ----------------------------------------------------------- fill types

type rawFillTypes struct {
	Types []struct {
		Name         string `xml:"name,attr"`
		Title        string `xml:"title,attr"`
		OnPriceTable bool   `xml:"showOnPriceTable,attr"`
		Bulk         bool   `xml:"isBulkType,attr"`
		Pallet       bool   `xml:"isPalletType,attr"`
		Bale         bool   `xml:"isBaleType,attr"`
		Physics      *struct {
			MassPerLiter float64 `xml:"massPerLiter,attr"`
		} `xml:"physics"`
		Economy *struct {
			PricePerLiter float64 `xml:"pricePerLiter,attr"`
			Factors       []struct {
				Period int     `xml:"period,attr"`
				Value  float64 `xml:"value,attr"`
			} `xml:"factors>factor"`
		} `xml:"economy"`
	} `xml:"fillTypes>fillType"`
	Categories []struct {
		Name  string `xml:"name,attr"`
		Fills string `xml:",chardata"`
	} `xml:"fillTypeCategories>fillTypeCategory"`
}

func (in *Install) parseFillTypes() {
	path := filepath.Join(in.Root, "data", "maps", "maps_fillTypes.xml")
	var raw rawFillTypes
	if err := decodeXML(path, &raw); err != nil {
		slog.Debug("gamedata: no fill types", "error", err)
		return
	}
	cats := map[string][]string{}
	for _, c := range raw.Categories {
		for _, name := range strings.Fields(c.Fills) {
			cats[name] = append(cats[name], c.Name)
		}
	}
	for _, t := range raw.Types {
		ft := FillType{
			Name:         t.Name,
			Title:        humanize(t.Title),
			OnPriceTable: t.OnPriceTable,
			Bulk:         t.Bulk,
			Pallet:       t.Pallet,
			Bale:         t.Bale,
			Categories:   cats[t.Name],
			Livestock:    isLivestock(t.Name, cats[t.Name]),
		}
		if ft.Title == "" {
			ft.Title = prettify(t.Name)
		}
		if p := t.Physics; p != nil {
			ft.MassPerLiter = p.MassPerLiter
		}
		if e := t.Economy; e != nil {
			ft.PricePerLiter = e.PricePerLiter
			for _, f := range e.Factors {
				if f.Period < 1 || f.Period > len(monthNames) {
					continue
				}
				if ft.Factors == nil {
					ft.Factors = make([]float64, len(monthNames))
				}
				ft.Factors[f.Period-1] = f.Value
			}
		}
		in.FillTypes = append(in.FillTypes, ft)
	}
}

// livestockPrefixes name the breed-per-species fill types; the bare names
// beside them are the species that ship only one breed.
var (
	livestockPrefixes = []string{"COW_", "PIG_", "SHEEP_", "HORSE_"}
	livestockNames    = map[string]bool{"CHICKEN": true, "CHICKEN_ROOSTER": true, "GOAT": true}
)

func isLivestock(name string, categories []string) bool {
	n := strings.ToUpper(name)
	if livestockNames[n] {
		return true
	}
	for _, p := range livestockPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	for _, c := range categories {
		if strings.EqualFold(c, "ANIMAL") || strings.EqualFold(c, "HORSE") {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ factories

type rawPlaceable struct {
	StoreData struct {
		Name     rawName `xml:"name"`
		Price    int     `xml:"price"`
		Category string  `xml:"category"`
	} `xml:"storeData"`
	Productions []struct {
		ID       string  `xml:"id,attr"`
		PerHour  float64 `xml:"cyclesPerHour,attr"`
		CostHour float64 `xml:"costsPerActiveHour,attr"`
		Inputs   []struct {
			FillType string  `xml:"fillType,attr"`
			Amount   float64 `xml:"amount,attr"`
		} `xml:"inputs>input"`
		Outputs []struct {
			FillType string  `xml:"fillType,attr"`
			Amount   float64 `xml:"amount,attr"`
		} `xml:"outputs>output"`
	} `xml:"productionPoint>productions>production"`
}

func (in *Install) parseFactories() {
	titles := map[string]string{}
	for _, ft := range in.FillTypes {
		titles[ft.Name] = ft.Title
	}
	title := func(name string) string {
		if t, ok := titles[name]; ok && t != "" {
			return t
		}
		return prettify(name)
	}

	dir := filepath.Join(in.Root, "data", "placeables")
	in.walkXML(dir, func(path, rel string) {
		var raw rawPlaceable
		if err := decodeXML(path, &raw); err != nil {
			slog.Debug("gamedata: skipped placeable", "file", rel, "error", err)
			in.Skipped++
			return
		}
		if len(raw.Productions) == 0 {
			return
		}
		f := Factory{
			File:  rel,
			Name:  storeName(raw.StoreData.Name),
			Price: raw.StoreData.Price,
		}
		if f.Name == "" {
			f.Name = prettify(strings.TrimSuffix(filepath.Base(rel), ".xml"))
		}
		for _, p := range raw.Productions {
			r := Recipe{ID: p.ID, PerHour: p.PerHour, CostHour: p.CostHour}
			for _, i := range p.Inputs {
				r.Inputs = append(r.Inputs, Ingredient{FillType: i.FillType, Title: title(i.FillType), Amount: i.Amount})
			}
			for _, o := range p.Outputs {
				r.Outputs = append(r.Outputs, Ingredient{FillType: o.FillType, Title: title(o.FillType), Amount: o.Amount})
			}
			f.Recipes = append(f.Recipes, r)
		}
		in.Factories = append(in.Factories, f)
	})

	sort.Slice(in.Factories, func(i, j int) bool {
		if in.Factories[i].Name != in.Factories[j].Name {
			return in.Factories[i].Name < in.Factories[j].Name
		}
		return in.Factories[i].File < in.Factories[j].File
	})
}

// ----------------------------------------------------------------- DLCs

func (in *Install) parseDLCs() {
	dir := filepath.Join(in.Root, "pdlc")
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Debug("gamedata: no pdlc directory", "error", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dlc") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		d := DLC{
			File:  "pdlc/" + e.Name(),
			Name:  name,
			Title: prettify(name),
		}
		if fi, err := e.Info(); err == nil {
			d.Size = fi.Size()
		}
		in.DLCs = append(in.DLCs, d)
	}
	sort.Slice(in.DLCs, func(i, j int) bool { return in.DLCs[i].Name < in.DLCs[j].Name })
}

// ---------------------------------------------------------------- utils

// walkXML visits every .xml file under dir. A missing directory is not an
// error: installs differ by platform and edition, and a missing corner
// costs the documents that corner would have produced, nothing more.
func (in *Install) walkXML(dir string, visit func(path, rel string)) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Debug("gamedata: walk error", "path", path, "error", err)
			// An unreadable subtree skips; it does not abort the parse.
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".xml") {
			return nil
		}
		rel, relErr := filepath.Rel(in.Root, path)
		if relErr != nil {
			rel = path
		}
		visit(path, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		slog.Debug("gamedata: walk failed", "dir", dir, "error", err)
	}
}

// humanize turns a game string into display text. Literal names pass
// through unchanged; `$l10n_fillType_riceLongGrain` becomes
// "Rice Long Grain" by dropping the key namespace and splitting
// camelCase. It is a fallback for strings whose translations are locked
// inside the encrypted archives, not a translator: identity always stays
// recoverable because callers keep the raw name beside the humanised one.
func humanize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "$") {
		return s // a literal store name, already display-ready
	}
	// Keys read `$l10n_<namespace>_<value>`. Only the two leading
	// segments are scaffolding: the value itself may contain underscores
	// ("$l10n_fillType_buffaloMilk_bottled"), so dropping everything up
	// to the *last* underscore would leave "Bottled" and lose the milk.
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "l10n_")
	if i := strings.Index(s, "_"); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return prettify(s)
}

// storeName resolves a store name template against its parameters.
//
// Three shapes occur in the game data and all three matter:
//
//	<name>1000 Vario</name>                        -> "1000 Vario"
//	<name params="8R">$l10n_shopItem_seriesX</name> -> "8R"
//	<name params="...|L180H">%s (%s)</name>         -> "Wheel Loader Fork (L180H)"
//
// The middle case is the one that would otherwise destroy identity:
// nineteen John Deere tractors share the key "seriesX" and are told apart
// only by the parameter, so when the template is an unresolvable key the
// parameters become the name.
func storeName(n rawName) string {
	text := strings.TrimSpace(n.Text)
	var params []string
	for _, p := range strings.Split(n.Params, "|") {
		if p = strings.TrimSpace(p); p != "" {
			params = append(params, humanize(p))
		}
	}

	if strings.Contains(text, "%s") {
		for _, p := range params {
			text = strings.Replace(text, "%s", p, 1)
		}
		// More placeholders than parameters: drop the leftovers rather
		// than printing "%s" at a reader.
		text = strings.ReplaceAll(text, "%s", "")
		return strings.Join(strings.Fields(text), " ")
	}
	if strings.HasPrefix(text, "$") && len(params) > 0 {
		return strings.Join(params, " ")
	}
	return humanize(text)
}

// prettify renders a raw game identifier ("wheat", "macDonPack",
// "RICE_BOXES") as display text. Unlike humanize it always rewrites,
// because identifiers are never display-ready. SCREAMING_SNAKE names are
// lowercased first: camel splitting cannot find word boundaries in
// "RICEBOXES", and shouting a fill type at the reader helps nobody.
func prettify(id string) string {
	s := strings.ReplaceAll(strings.TrimSpace(id), "_", " ")
	if !hasLower(s) {
		s = strings.ToLower(s)
	}
	return titleCase(splitCamel(s))
}

func hasLower(s string) bool {
	for _, r := range s {
		if isLower(r) {
			return true
		}
	}
	return false
}

// splitCamel inserts spaces at lower->upper and letter->digit boundaries
// so "riceLongGrain" reads as three words and "vario1000" as two.
func splitCamel(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			switch {
			case isLower(prev) && isUpper(r):
				b.WriteRune(' ')
			case isDigit(prev) != isDigit(r) && (isLetter(prev) || isLetter(r)):
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		if isLower(r[0]) {
			r[0] = r[0] - 'a' + 'A'
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

func isLower(r rune) bool  { return r >= 'a' && r <= 'z' }
func isUpper(r rune) bool  { return r >= 'A' && r <= 'Z' }
func isDigit(r rune) bool  { return r >= '0' && r <= '9' }
func isLetter(r rune) bool { return isLower(r) || isUpper(r) }

// brandName normalises the brand constant. NONE means brandless, which
// is worth saying as nothing at all, and the case is forced because the
// game data spells the same marque both "MASSEYFERGUSON" and
// "masseyFerguson" — two brands in any list that does not normalise.
func brandName(brand string) string {
	b := strings.TrimSpace(brand)
	if b == "" || strings.EqualFold(b, "NONE") {
		return ""
	}
	return strings.ToUpper(b)
}

// num formats a float without trailing zeros, so working widths read
// "3 m" and "10.5 m" rather than "3.000000 m".
func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
