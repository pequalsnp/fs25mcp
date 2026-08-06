package fs25data

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bom is the UTF-8 byte order mark the game writes ahead of most XML
// declarations. encoding/xml rejects it outright, so at least one fixture
// file carries it deliberately. Written as an escape because Go only
// tolerates a byte order mark at the very start of a source file, so it
// is spelled out in bytes here.
var bom = string([]byte{0xEF, 0xBB, 0xBF})

// writeInstall builds a miniature but structurally faithful FS25 install
// in a temp dir: same directory layout, same element shapes, same
// quirks (BOM, l10n keys, parameterised names, service fill units,
// duplicated production points). Fixtures are written at test time rather
// than committed, since committing game files would be redistribution.
func writeInstall(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"VERSION": "1.2.3.4",

		"data/vehicles/fendt/vario1000/vario1000.xml": bom + `<?xml version="1.0" encoding="utf-8"?>
<vehicle type="tractor">
  <storeData>
    <name>1000 Vario</name>
    <specs><power>396</power><maxSpeed>60</maxSpeed></specs>
    <functions><function>$l10n_function_tractor</function></functions>
    <price>382000</price>
    <lifetime>600</lifetime>
    <brand>FENDT</brand>
    <category>tractorsL</category>
  </storeData>
  <base><components><component mass="11400"/><component mass="600"/></components></base>
  <fillUnit><fillUnitConfigurations><fillUnitConfiguration><fillUnits>
    <fillUnit fillTypes="diesel" capacity="600"/>
    <fillUnit fillTypes="air" capacity="4000"/>
  </fillUnits></fillUnitConfiguration></fillUnitConfigurations></fillUnit>
</vehicle>`,

		// Identity lives in the params attribute, not the name text.
		"data/vehicles/johnDeere/series8R/series8R.xml": `<?xml version="1.0"?>
<vehicle type="tractor">
  <storeData>
    <name params="8R">$l10n_shopItem_seriesX</name>
    <specs><power>326</power><maxSpeed>50</maxSpeed></specs>
    <price>312500</price>
    <brand>masseyFerguson</brand>
    <category>tractorsL</category>
  </storeData>
  <base><componentConfigurations><componentConfiguration><component mass="11200"/></componentConfiguration></componentConfigurations></base>
</vehicle>`,

		"data/vehicles/krone/tx460/tx460.xml": `<?xml version="1.0"?>
<vehicle type="trailer">
  <storeData>
    <name>TX 460 D</name>
    <specs><neededPower>180</neededPower><workingWidth>3</workingWidth></specs>
    <functions><function>$l10n_function_tipper</function></functions>
    <price>65500</price>
    <brand>KRONE</brand>
    <category>trailers</category>
  </storeData>
  <base><components><component mass="9830"/></components></base>
  <fillUnit><fillUnitConfigurations><fillUnitConfiguration><fillUnits>
    <fillUnit fillTypeCategories="BULK" capacity="30000"/>
    <fillUnit fillTypes="def" capacity="45"/>
  </fillUnits></fillUnitConfiguration></fillUnitConfigurations></fillUnit>
</vehicle>`,

		// A fuel bowser: its only fill unit is a service fluid, and that
		// fluid is the entire point of the vehicle.
		"data/vehicles/thunderCreek/fst990/fst990.xml": `<?xml version="1.0"?>
<vehicle type="trailer">
  <storeData>
    <name>FST 990</name>
    <price>32000</price>
    <brand>THUNDERCREEK</brand>
    <category>misc</category>
  </storeData>
  <base><components><component mass="2600"/></components></base>
  <fillUnit><fillUnitConfigurations><fillUnitConfiguration><fillUnits>
    <fillUnit fillTypes="diesel" capacity="3750"/>
  </fillUnits></fillUnitConfiguration></fillUnitConfigurations></fillUnit>
</vehicle>`,

		// The unwrapped fill unit shape: no fillUnitConfigurations layer.
		// FS25 1.21 never writes this, but a patch that dropped the layer
		// must degrade to "capacity parsed", not "capacity zero".
		"data/vehicles/brantner/dd24073/dd24073.xml": `<?xml version="1.0"?>
<vehicle type="trailer">
  <storeData>
    <name>DD 24073/2 XXL</name>
    <specs><neededPower>120</neededPower></specs>
    <price>39500</price>
    <brand>BRANTNER</brand>
    <category>trailers</category>
  </storeData>
  <base><components><component mass="6470"/></components></base>
  <fillUnit><fillUnits>
    <fillUnit fillTypeCategories="BULK" capacity="24000"/>
  </fillUnits></fillUnit>
</vehicle>`,

		// A store-pack umbrella tile: a name, a category, no facts.
		"data/vehicles/agco/series8000/series8000.xml": `<?xml version="1.0"?>
<vehicle type="tractor">
  <storeData><name>8010 Series</name><brand>AGCO</brand><category>tractorsM</category></storeData>
</vehicle>`,

		// Not a store item at all — a shared config fragment.
		"data/vehicles/shared/wheels/wheel001.xml": `<?xml version="1.0"?><wheel><physics/></wheel>`,

		"data/maps/maps_fruitTypes.xml": bom + `<?xml version="1.0"?>
<map>
  <fruitTypes>
    <fruitType filename="$data/foliage/wheat/wheat.xml"/>
    <fruitType filename="$data/foliage/poplar/poplar.xml"/>
    <fruitType filename="$dataS/foliage/secret/secret.xml"/>
    <fruitType filename="$data/foliage/missing/missing.xml"/>
  </fruitTypes>
  <fruitTypeCategories>
    <fruitTypeCategory name="GRAINHEADER">WHEAT</fruitTypeCategory>
    <fruitTypeCategory name="SOWINGMACHINE">WHEAT</fruitTypeCategory>
  </fruitTypeCategories>
  <fruitTypeConverters>
    <fruitTypeConverter name="FORAGEHARVESTER">
      <converter from="WHEAT" to="CHAFF" factor="4.0"/>
    </fruitTypeConverter>
  </fruitTypeConverters>
</map>`,

		"data/foliage/wheat/wheat.xml": bom + `<?xml version="1.0"?>
<foliageType>
  <fruitType name="wheat" shownOnMap="true" useForFieldMissions="true">
    <windrow fillType="straw" litersPerSqm="3.68" cutFillType="WHEAT_CUT"/>
    <harvest litersPerSqm="0.57" cutHeight="0.15" chopperType="CHOPPER_STRAW"/>
    <growth growthRequiresLime="true"/>
    <soil lowDensityRequired="true" consumesLime="true"/>
    <seeding litersPerSqm="0.0308" needsRolling="true" isAvailable="true"/>
    <cultivation isAllowed="true"/>
  </fruitType>
</foliageType>`,

		// Declares neither <seeding> nor <cultivation>: the game said
		// nothing, so the documents must say nothing either.
		"data/foliage/poplar/poplar.xml": `<?xml version="1.0"?>
<foliageType>
  <fruitType name="poplar" shownOnMap="true">
    <harvest litersPerSqm="1.2"/>
  </fruitType>
</foliageType>`,

		"data/maps/maps_fillTypes.xml": bom + `<?xml version="1.0"?>
<map>
  <fillTypes>
    <fillType name="WHEAT" title="$l10n_fillType_wheat" showOnPriceTable="true" isBulkType="true">
      <physics massPerLiter="0.78"/>
      <economy pricePerLiter="0.337">
        <factors>
          <factor period="1" value="1.00"/>
          <factor period="6" value="0.81"/>
          <factor period="11" value="1.21"/>
        </factors>
      </economy>
    </fillType>
    <fillType name="BUFFALOMILK_BOTTLED" title="$l10n_fillType_buffaloMilk_bottled" showOnPriceTable="true" isPalletType="true">
      <physics massPerLiter="1.03"/>
      <economy pricePerLiter="2.49"/>
    </fillType>
    <fillType name="COW_ANGUS" title="$l10n_fillType_cowAngus" showOnPriceTable="false">
      <physics massPerLiter="600"/>
      <economy pricePerLiter="5000"/>
    </fillType>
  </fillTypes>
  <fillTypeCategories>
    <fillTypeCategory name="BULK">WHEAT</fillTypeCategory>
    <fillTypeCategory name="ANIMAL">COW_ANGUS</fillTypeCategory>
  </fillTypeCategories>
</map>`,

		"data/placeables/brandless/bakery/bakery.xml": `<?xml version="1.0"?>
<placeable type="productionPoint">
  <storeData><name>$l10n_shopItem_bakery</name><price>150000</price><category>productionPoints</category></storeData>
  <productionPoint><productions>
    <production id="bread" cyclesPerHour="5" costsPerActiveHour="2.5">
      <inputs><input fillType="FLOUR" amount="90"/></inputs>
      <outputs><output fillType="BREAD" amount="45"/></outputs>
    </production>
  </productions></productionPoint>
</placeable>`,

		// The same bakery again at a different size: identical recipe,
		// different build price. Must merge, not duplicate.
		"data/placeables/brandless/bakerySmall/bakerySmall.xml": `<?xml version="1.0"?>
<placeable type="productionPoint">
  <storeData><name>$l10n_shopItem_bakery</name><price>36000</price><category>productionPoints</category></storeData>
  <productionPoint><productions>
    <production id="bread" cyclesPerHour="5" costsPerActiveHour="2.5">
      <inputs><input fillType="FLOUR" amount="90"/></inputs>
      <outputs><output fillType="BREAD" amount="45"/></outputs>
    </production>
  </productions></productionPoint>
</placeable>`,

		// A placeable with no productions contributes nothing.
		"data/placeables/brandless/shed/shed.xml": `<?xml version="1.0"?>
<placeable type="shed"><storeData><name>Shed</name><price>1000</price></storeData></placeable>`,

		"pdlc/macDonPack.dlc": "GAR encrypted payload",
		"pdlc/readme.txt":     "not a dlc",

		// Looks like content, is not parseable: pins the "skip and
		// count, never fail the run" contract.
		"data/vehicles/broken/broken.xml": "<vehicle><storeData><name>oops",
	}

	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func parseFixture(t *testing.T) *Install {
	t.Helper()
	in, err := Parse(writeInstall(t))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	return in
}

func findItem(t *testing.T, in *Install, name string) StoreItem {
	t.Helper()
	for _, it := range in.StoreItems {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("Parse() store items = %v, want one named %q", itemNames(in), name)
	return StoreItem{}
}

func itemNames(in *Install) []string {
	out := make([]string, 0, len(in.StoreItems))
	for _, it := range in.StoreItems {
		out = append(out, it.Name)
	}
	return out
}

func TestParseInstallShape(t *testing.T) {
	in := parseFixture(t)

	cases := []struct {
		field string
		got   any
		want  any
	}{
		{"Version", in.Version, "1.2.3.4"},
		{"len(StoreItems)", len(in.StoreItems), 5},
		{"len(Crops)", len(in.Crops), 2},
		{"len(FillTypes)", len(in.FillTypes), 3},
		{"len(Factories)", len(in.Factories), 2},
		{"len(DLCs)", len(in.DLCs), 1},
		{"Skipped", in.Skipped, 1},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("Parse() %s = %v, want %v", c.field, c.got, c.want)
			}
		})
	}
}

func TestParseStoreItem(t *testing.T) {
	in := parseFixture(t)

	t.Run("literal name and summed component mass", func(t *testing.T) {
		it := findItem(t, in, "1000 Vario")
		cases := []struct {
			field string
			got   any
			want  any
		}{
			{"Brand", it.Brand, "FENDT"},
			{"Group", it.Group, "tractors"},
			{"Price", it.Price, 382000},
			{"Power", it.Power, 396.0},
			{"MaxSpeed", it.MaxSpeed, 60.0},
			{"Weight", it.Weight, 12000.0},
			{"Function", it.Function, "Tractor"},
			// Diesel and air are not payload; the fuel tank is reported
			// as a tank and the brake reservoir not at all.
			{"Capacity", it.Capacity, 0.0},
			{"TankCapacity", it.TankCapacity, 600.0},
			{"TankFills", it.TankFills, "diesel"},
		}
		for _, c := range cases {
			if c.got != c.want {
				t.Errorf("Parse() %s = %v, want %v", c.field, c.got, c.want)
			}
		}
	})

	t.Run("name resolved from params, mass from configuration", func(t *testing.T) {
		it := findItem(t, in, "8R")
		if it.Weight != 11200 {
			t.Errorf("Parse() Weight = %v, want %v", it.Weight, 11200.0)
		}
		// The game spells this marque two ways; the parser must not.
		if it.Brand != "MASSEYFERGUSON" {
			t.Errorf("Parse() Brand = %q, want %q", it.Brand, "MASSEYFERGUSON")
		}
	})

	t.Run("payload capacity skips the service tank", func(t *testing.T) {
		it := findItem(t, in, "TX 460 D")
		if it.Capacity != 30000 {
			t.Errorf("Parse() Capacity = %v, want %v", it.Capacity, 30000.0)
		}
		if it.CapacityFills != "bulk" {
			t.Errorf("Parse() CapacityFills = %q, want %q", it.CapacityFills, "bulk")
		}
		if it.Group != "transport" {
			t.Errorf("Parse() Group = %q, want %q", it.Group, "transport")
		}
	})

	t.Run("a fuel bowser reports the fluid it exists to carry", func(t *testing.T) {
		it := findItem(t, in, "FST 990")
		if it.Capacity != 0 {
			t.Errorf("Parse() Capacity = %v, want 0 (diesel is a tank, not a payload)", it.Capacity)
		}
		if it.TankCapacity != 3750 {
			t.Errorf("Parse() TankCapacity = %v, want %v", it.TankCapacity, 3750.0)
		}
	})

	t.Run("fill units without a configuration layer still parse", func(t *testing.T) {
		it := findItem(t, in, "DD 24073/2 XXL")
		if it.Capacity != 24000 {
			t.Errorf("Parse() Capacity = %v, want %v", it.Capacity, 24000.0)
		}
		if it.CapacityFills != "bulk" {
			t.Errorf("Parse() CapacityFills = %q, want %q", it.CapacityFills, "bulk")
		}
	})

	t.Run("factless store pack tiles are dropped", func(t *testing.T) {
		for _, it := range in.StoreItems {
			if it.Name == "8010 Series" {
				t.Errorf("Parse() kept %q, want it dropped as a store-pack tile", it.Name)
			}
		}
	})
}

func TestParseCrop(t *testing.T) {
	in := parseFixture(t)
	if len(in.Crops) != 2 {
		t.Fatalf("Parse() crops = %d, want 2", len(in.Crops))
	}
	c := in.Crops[0]
	if c.Name != "wheat" {
		t.Fatalf("Parse() first crop = %q, want wheat (index order follows the fruit type index)", c.Name)
	}

	cases := []struct {
		field string
		got   any
		want  any
	}{
		{"Name", c.Name, "wheat"},
		{"Title", c.Title, "Wheat"},
		{"YieldPerSqm", c.YieldPerSqm, 0.57},
		{"SeedPerSqm", c.SeedPerSqm, 0.0308},
		{"WindrowFill", c.WindrowFill, "straw"},
		{"NeedsRolling", c.NeedsRolling, true},
		{"RequiresLime", c.RequiresLime, true},
		{"ConsumesLime", c.ConsumesLime, true},
		{"Sowable", c.Sowable, true},
		{"SowableDeclared", c.SowableDeclared, true},
		{"CultivateDeclared", c.CultivateDeclared, true},
		{"len(Headers)", len(c.Headers), 1},
		{"len(Planters)", len(c.Planters), 1},
		{"len(Conversions)", len(c.Conversions), 1},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("Parse() crop %s = %v, want %v", tc.field, tc.got, tc.want)
			}
		})
	}

	// A sowing machine sows wheat; it does not harvest it.
	if c.Headers[0] != "GRAINHEADER" {
		t.Errorf("Parse() crop Headers[0] = %q, want %q", c.Headers[0], "GRAINHEADER")
	}
	if c.Planters[0] != "SOWINGMACHINE" {
		t.Errorf("Parse() crop Planters[0] = %q, want %q", c.Planters[0], "SOWINGMACHINE")
	}

	// An absent element is "the game did not say", not "no".
	t.Run("undeclared seeding and cultivation stay unknown", func(t *testing.T) {
		p := in.Crops[1]
		if p.Name != "poplar" {
			t.Fatalf("Parse() second crop = %q, want poplar", p.Name)
		}
		if p.SowableDeclared {
			t.Errorf("Parse() SowableDeclared = true, want false (no <seeding> element)")
		}
		if p.CultivateDeclared {
			t.Errorf("Parse() CultivateDeclared = true, want false (no <cultivation> element)")
		}
	})
}

func TestParseFillTypes(t *testing.T) {
	in := parseFixture(t)
	byName := map[string]FillType{}
	for _, ft := range in.FillTypes {
		byName[ft.Name] = ft
	}

	cases := []struct {
		name  string
		field string
		got   any
		want  any
	}{
		{"WHEAT", "Title", byName["WHEAT"].Title, "Wheat"},
		{"WHEAT", "PricePerLiter", byName["WHEAT"].PricePerLiter, 0.337},
		{"WHEAT", "MassPerLiter", byName["WHEAT"].MassPerLiter, 0.78},
		{"WHEAT", "Bulk", byName["WHEAT"].Bulk, true},
		{"WHEAT", "Livestock", byName["WHEAT"].Livestock, false},
		{"WHEAT", "Factors[0]", byName["WHEAT"].Factors[0], 1.00},
		{"WHEAT", "Factors[10]", byName["WHEAT"].Factors[10], 1.21},
		// The value half of the key has its own underscore; keeping only
		// the last segment would name this "Bottled".
		{"BUFFALOMILK_BOTTLED", "Title", byName["BUFFALOMILK_BOTTLED"].Title, "Buffalo Milk Bottled"},
		{"COW_ANGUS", "Livestock", byName["COW_ANGUS"].Livestock, true},
		{"COW_ANGUS", "OnPriceTable", byName["COW_ANGUS"].OnPriceTable, false},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.field, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("Parse() fill type %s %s = %v, want %v", c.name, c.field, c.got, c.want)
			}
		})
	}
}

func TestParseFactoriesAndDLC(t *testing.T) {
	in := parseFixture(t)

	if len(in.Factories) != 2 {
		t.Fatalf("Parse() factories = %d, want 2", len(in.Factories))
	}
	f := in.Factories[0]
	if f.Name != "Bakery" {
		t.Errorf("Parse() factory Name = %q, want %q", f.Name, "Bakery")
	}
	if len(f.Recipes) != 1 {
		t.Fatalf("Parse() factory recipes = %d, want 1", len(f.Recipes))
	}
	r := f.Recipes[0]
	if len(r.Inputs) != 1 || r.Inputs[0].FillType != "FLOUR" || r.Inputs[0].Amount != 90 {
		t.Errorf("Parse() recipe inputs = %+v, want 90 l of FLOUR", r.Inputs)
	}
	if len(r.Outputs) != 1 || r.Outputs[0].Title != "Bread" {
		t.Errorf("Parse() recipe outputs = %+v, want Bread", r.Outputs)
	}

	if len(in.DLCs) != 1 {
		t.Fatalf("Parse() dlcs = %d, want 1 (readme.txt is not a pack)", len(in.DLCs))
	}
	if got, want := in.DLCs[0].Title, "Mac Don Pack"; got != want {
		t.Errorf("Parse() dlc Title = %q, want %q", got, want)
	}
	if got, want := in.DLCs[0].Name, "macDonPack"; got != want {
		t.Errorf("Parse() dlc Name = %q, want %q", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(file, []byte("1.0"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		root string
		want string
	}{
		{"empty path", "  ", "root path is required"},
		{"missing directory", filepath.Join(dir, "nope"), "stat install root"},
		{"not a directory", file, "is not a directory"},
		{"no data directory", dir, "has no data/ directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.root)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want one containing %q", c.root, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", c.root, err.Error(), c.want)
			}
		})
	}
}

func TestStoreName(t *testing.T) {
	cases := []struct {
		name string
		in   rawName
		want string
	}{
		{"literal", rawName{Text: "1000 Vario"}, "1000 Vario"},
		{"key with params is the params", rawName{Text: "$l10n_shopItem_seriesX", Params: "8R"}, "8R"},
		{"multi params", rawName{Text: "$l10n_shopItem_seriesXAdd", Params: "6M|(95-125)"}, "6M (95-125)"},
		{"key without params", rawName{Text: "$l10n_shopItem_bakery"}, "Bakery"},
		{"template substitution", rawName{Text: "%s (%s)", Params: "$l10n_storeItem_bagLifter|L180H"}, "Bag Lifter (L180H)"},
		{"template with literal text", rawName{Text: "Miniair Nova %s", Params: "$l10n_configuration_rigid"}, "Miniair Nova Rigid"},
		{"unfilled placeholders drop", rawName{Text: "%s %s (%s)", Params: "Front|Loader"}, "Front Loader ()"},
		{"empty", rawName{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := storeName(c.in); got != c.want {
				t.Errorf("storeName(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHumanize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"literal passes through", "MF 8570", "MF 8570"},
		{"camel case key", "$l10n_fillType_riceLongGrain", "Rice Long Grain"},
		{"underscore in the value", "$l10n_fillType_buffaloMilk_bottled", "Buffalo Milk Bottled"},
		{"function key", "$l10n_function_tractorFrontloader", "Tractor Frontloader"},
		{"digits split from letters", "$l10n_shopItem_vario1000", "Vario 1000"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := humanize(c.in); got != c.want {
				t.Errorf("humanize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsServiceFill(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"diesel", "diesel", true},
		{"def is case insensitive", "DEF", true},
		{"silage additive", "silage_additive", true},
		// Air is the brake reservoir, handled separately: it is not a
		// tank worth reporting at all.
		{"air is not a service tank", "air", false},
		{"bulk payload", "BULK", false},
		{"mixed payload wins", "diesel wheat", false},
		{"unspecified", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isServiceFill(c.in); got != c.want {
				t.Errorf("isServiceFill(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsAirFill(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"brake reservoir", "air", true},
		{"case insensitive", "AIR", true},
		{"diesel is not air", "diesel", false},
		{"payload is not air", "bulk", false},
		{"unspecified", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAirFill(c.in); got != c.want {
				t.Errorf("isAirFill(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsLivestock(t *testing.T) {
	cases := []struct {
		name       string
		fill       string
		categories []string
		want       bool
	}{
		{"breed prefix", "COW_ANGUS", nil, true},
		{"single species", "CHICKEN", nil, true},
		{"category only", "SOMETHING", []string{"ANIMAL"}, true},
		{"animal product is not livestock", "GOATMILK", []string{"BULK"}, false},
		{"crop", "WHEAT", []string{"BULK"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isLivestock(c.fill, c.categories); got != c.want {
				t.Errorf("isLivestock(%q, %v) = %v, want %v", c.fill, c.categories, got, c.want)
			}
		})
	}
}
