package fs25save

import (
	"os"
	"path/filepath"
	"testing"
)

// write lays down a minimal savegame. The BOM is deliberate: the game
// writes one and encoding/xml rejects it, so a parser that works on
// hand-written fixtures and not on real saves is the trap here.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), append([]byte("\xEF\xBB\xBF"), body...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadSaveProvenanceAndMoney(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "careerSavegame.xml", `<careerSavegame>
  <settings><savegameName>Canada Grain Farmer</savegameName><mapTitle>Gnadenthal</mapTitle>
  <mapId>FS25_Gnadenthal_crossplay.Gnadenthal</mapId><saveDate>2025/05/26</saveDate>
  <economicDifficulty>NORMAL</economicDifficulty></settings>
  <statistics><playTime>3731.5</playTime></statistics></careerSavegame>`)
	// Farm 0 is the unowned bucket and must not be mistaken for the
	// player's balance. The players block carries personal identifiers
	// and must never reach the output.
	write(t, dir, "farms.xml", `<farms>
  <farm farmId="0" name="Unowned" money="999999" loan="0"/>
  <farm farmId="1" name="My farm" money="2286776" loan="1500">
    <players><player uniqueUserId="SECRET" lastNickname="kyles"/></players>
  </farm></farms>`)
	write(t, dir, "farmland.xml", `<farmlands>
  <farmland id="1" farmId="0"/><farmland id="2" farmId="1"/><farmland id="3" farmId="1"/></farmlands>`)
	write(t, dir, "vehicles.xml", `<vehicles><vehicle/><vehicle/></vehicles>`)
	write(t, dir, "placeables.xml", `<placeables>
  <placeable isPreplaced="true" uniqueId="preplaced_grainFlourMillUS_c929" price="3500000" farmId="1">
    <productionPoint>
      <production id="flourWheat" isEnabled="true"/>
      <production id="flourOat" isEnabled="false"/>
      <storage farmId="1"><node fillType="WHEAT" fillLevel="633247.3"/><node fillType="FLOUR" fillLevel="0"/></storage>
    </productionPoint></placeable>
  <placeable modName="FS25_BGAFD" filename="$moddir$FS25_BGAFD/B_extremeGame.xml" uniqueId="placeable3b54" price="100000" farmId="1">
    <productionPoint><production id="biogas" isEnabled="true"/></productionPoint></placeable>
  <placeable isPreplaced="true" uniqueId="preplaced_limeStation_4226" price="1" farmId="0"/>
</placeables>`)

	s, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if s.Money != 2286776 || s.Loan != 1500 {
		t.Errorf("money/loan = %v/%v, want 2286776/1500 (farm 0 is not the player)", s.Money, s.Loan)
	}
	if !s.MapIsMod {
		t.Error("MapIsMod = false; an FS25_-prefixed mapId is a mod map")
	}
	if s.FarmlandOwned != 2 || s.FarmlandTotal != 3 {
		t.Errorf("farmland = %d/%d, want 2/3", s.FarmlandOwned, s.FarmlandTotal)
	}
	if s.Vehicles != 2 {
		t.Errorf("vehicles = %d, want 2", s.Vehicles)
	}

	// Only farm-1 production points, and the lime station has none.
	if len(s.Productions) != 2 {
		t.Fatalf("productions = %d, want 2", len(s.Productions))
	}

	byKind := map[string]Production{}
	for _, p := range s.Productions {
		byKind[p.Kind] = p
	}

	mill, ok := byKind["grainFlourMillUS"]
	if !ok {
		t.Fatalf("no mill; got kinds %v", byKind)
	}
	if mill.Source != SourceBaseGame || mill.Mod != "" {
		t.Errorf("mill source = %q mod = %q, want base game", mill.Source, mill.Mod)
	}
	if len(mill.Enabled) != 1 || mill.Enabled[0] != "flourWheat" {
		t.Errorf("mill enabled = %v, want [flourWheat]", mill.Enabled)
	}
	if len(mill.Disabled) != 1 || mill.Disabled[0] != "flourOat" {
		t.Errorf("mill disabled = %v, want [flourOat]", mill.Disabled)
	}
	// A zero fill level is not storage; reporting it invites "you have
	// flour" when there is none.
	if _, has := mill.Storage["FLOUR"]; has {
		t.Error("zero-level fill type should not appear in storage")
	}
	if mill.Storage["WHEAT"] == 0 {
		t.Error("wheat storage missing")
	}

	// The whole point: the save is the one place that knows a building
	// came from a mod, because mod content is in no store document.
	bga, ok := byKind["B_extremeGame"]
	if !ok {
		t.Fatalf("no mod building; got kinds %v", byKind)
	}
	if bga.Mod != "FS25_BGAFD" || bga.Source != "mod: FS25_BGAFD" {
		t.Errorf("mod building source = %q / %q, want mod: FS25_BGAFD", bga.Mod, bga.Source)
	}
}
