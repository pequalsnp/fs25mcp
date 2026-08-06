// Package plan computes production economics from the installed game's
// own data.
//
// It exists because of a measured failure. Asked which production chain
// was most profitable, an expert quoted the Rice Box recipe correctly —
// 100 l Rice in, 45 l Rice Box out, 10 cycles/hour — and the output price
// correctly at 2.73/l, both verbatim from the game files. Then it called
// that "a 23% markup". Rice costs 1.1/l, not the 1.0 it silently assumed,
// so the real figure is about 12%. Correct facts, wrong arithmetic, and
// the answer read exactly like the grounded ones.
//
// A language model asked to divide two numbers it did not look up will
// sometimes invent one of them. Computing it here makes that impossible
// rather than discouraged: both sides come from the same parse, and the
// margin is division, not recall.
package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pequalsnp/fs25mcp/internal/fs25data"
)

// Economics is one recipe costed against the game's own fill-type prices.
//
// Every monetary field is in game currency at BASE price — the price
// before a selling point's own multiplier and before the month factor.
// That is the only honest common denominator: a sell price at a specific
// station in a specific month is a different question, and pretending
// otherwise is how a 12% margin gets reported as 23%.
type Economics struct {
	// Source is always "base game", and saying so is the point. This
	// package parses the INSTALL TREE only: DLC ships as encrypted
	// pdlc/*.dlc whose contents cannot be read, and mods live in a
	// separate folder this never touches. So a production point costed
	// here is one every player owns — which is exactly the claim an
	// expert must be able to make without hedging, and exactly the claim
	// it must NOT make about anything sourced from a mod document.
	Source      string  `json:"source"`
	Factory     string  `json:"factory"`
	FactoryCost int     `json:"factory_cost"`
	Recipe      string  `json:"recipe"`
	Inputs      string  `json:"inputs"`
	Outputs     string  `json:"outputs"`
	InputCost   float64 `json:"input_cost_per_cycle"`
	OutputValue float64 `json:"output_value_per_cycle"`
	// GrossPerCycle is output value less input cost, before running cost.
	GrossPerCycle float64 `json:"gross_per_cycle"`
	CyclesPerHour float64 `json:"cycles_per_hour"`
	RunningCost   float64 `json:"running_cost_per_hour"`
	// NetPerHour is the number that actually answers "is this worth
	// building": gross per cycle times throughput, less the running cost
	// the factory charges whether or not you are watching it.
	NetPerHour float64 `json:"net_per_hour"`
	// MarginPct is the markup over input cost. Zero-input recipes (a
	// greenhouse making saplings from water the game prices at nothing)
	// have no meaningful markup, so this is omitted rather than reported
	// as infinite.
	MarginPct *float64 `json:"margin_pct,omitempty"`
	// PaybackHours is factory cost divided by net per hour, i.e. how long
	// it runs before it has paid for itself. Omitted when it never does.
	PaybackHours *float64 `json:"payback_hours,omitempty"`
}

// SourceBaseGame labels everything this package emits. See Economics.Source.
const SourceBaseGame = "base game"

// Economy indexes an install's fill-type prices so recipes can be costed.
type Economy struct {
	price map[string]float64
	title map[string]string
}

// NewEconomy indexes prices by the fill-type name recipes refer to.
func NewEconomy(in *fs25data.Install) *Economy {
	e := &Economy{price: map[string]float64{}, title: map[string]string{}}
	for _, ft := range in.FillTypes {
		key := strings.ToUpper(ft.Name)
		e.price[key] = ft.PricePerLiter
		e.title[key] = ft.Title
	}
	return e
}

// Price returns a fill type's base price per litre and whether the game
// priced it at all. A missing fill type is NOT zero: treating "the game
// never said" as free is what turns an unprofitable chain into a
// spectacular one.
func (e *Economy) Price(fillType string) (float64, bool) {
	p, ok := e.price[strings.ToUpper(fillType)]
	return p, ok
}

// Cost prices one side of a recipe. It reports incomplete when any
// ingredient has no price, so the caller can drop the recipe rather than
// quote a number built on a silent zero.
func (e *Economy) Cost(ings []fs25data.Ingredient) (total float64, complete bool) {
	complete = true
	for _, ing := range ings {
		p, ok := e.Price(ing.FillType)
		if !ok {
			complete = false
			continue
		}
		total += p * ing.Amount
	}
	return total, complete
}

// Recipes costs every recipe in the install, best net-per-hour first.
// Recipes with an unpriced ingredient are omitted entirely — see Cost.
func Recipes(in *fs25data.Install) []Economics {
	e := NewEconomy(in)
	var out []Economics
	// The install describes the same production point from more than one
	// placeable file — 338 recipe rows collapse to 168 distinct ones — so
	// a ranking built without this shows the same biogas plant five times
	// and buries the alternatives.
	seen := map[string]bool{}
	for _, f := range in.Factories {
		for _, r := range f.Recipes {
			key := f.Name + "|" + r.ID + "|" + describe(r.Inputs) + "|" + describe(r.Outputs)
			if seen[key] {
				continue
			}
			seen[key] = true
			inCost, inOK := e.Cost(r.Inputs)
			outVal, outOK := e.Cost(r.Outputs)
			if !inOK || !outOK {
				continue
			}
			ec := Economics{
				Source:        SourceBaseGame,
				Factory:       f.Name,
				FactoryCost:   f.Price,
				Recipe:        r.ID,
				Inputs:        describe(r.Inputs),
				Outputs:       describe(r.Outputs),
				InputCost:     round2(inCost),
				OutputValue:   round2(outVal),
				GrossPerCycle: round2(outVal - inCost),
				CyclesPerHour: r.PerHour,
				RunningCost:   r.CostHour,
				NetPerHour:    round2((outVal-inCost)*r.PerHour - r.CostHour),
			}
			if inCost > 0 {
				m := round2((outVal/inCost - 1) * 100)
				ec.MarginPct = &m
			}
			if ec.NetPerHour > 0 && f.Price > 0 {
				h := round2(float64(f.Price) / ec.NetPerHour)
				ec.PaybackHours = &h
			}
			out = append(out, ec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NetPerHour > out[j].NetPerHour })
	return out
}

// Matching filters costed recipes by a case-insensitive substring across
// the factory, recipe and ingredient names.
func Matching(in *fs25data.Install, query string) []Economics {
	all := Recipes(in)
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	var out []Economics
	for _, ec := range all {
		hay := strings.ToLower(ec.Factory + " " + ec.Recipe + " " + ec.Inputs + " " + ec.Outputs)
		if strings.Contains(hay, q) {
			out = append(out, ec)
		}
	}
	return out
}

func describe(ings []fs25data.Ingredient) string {
	parts := make([]string, 0, len(ings))
	for _, ing := range ings {
		name := ing.Title
		if name == "" {
			name = ing.FillType
		}
		parts = append(parts, fmt.Sprintf("%g l %s", ing.Amount, name))
	}
	return strings.Join(parts, " + ")
}

func round2(f float64) float64 {
	return float64(int64(f*100+sign(f)*0.5)) / 100
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
