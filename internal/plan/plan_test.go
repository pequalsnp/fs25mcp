package plan

import (
	"testing"

	"github.com/pequalsnp/fs25mcp/internal/fs25data"
)

// The regression this package exists for. An expert quoted the Rice Box
// recipe and its output price correctly, straight from the game files,
// then assumed rice cost 1.0/l when it costs 1.1 and reported the markup
// as 23%. It is 11.68%. The numbers below are the real ones from a 1.21
// install.
func TestRiceBoxMarginUsesTheRealInputPrice(t *testing.T) {
	in := &fs25data.Install{
		FillTypes: []fs25data.FillType{
			{Name: "RICE", Title: "Rice", PricePerLiter: 1.1},
			{Name: "RICE_BOXES", Title: "Rice Box", PricePerLiter: 2.73},
		},
		Factories: []fs25data.Factory{{
			Name:  "Canned Packaged Factory",
			Price: 330000,
			Recipes: []fs25data.Recipe{{
				ID:       "preservedFoodRiceBoxes",
				Inputs:   []fs25data.Ingredient{{FillType: "RICE", Title: "Rice", Amount: 100}},
				Outputs:  []fs25data.Ingredient{{FillType: "RICE_BOXES", Title: "Rice Box", Amount: 45}},
				PerHour:  10,
				CostHour: 2,
			}},
		}},
	}

	got := Recipes(in)
	if len(got) != 1 {
		t.Fatalf("got %d costed recipes, want 1", len(got))
	}
	ec := got[0]

	if ec.Source != SourceBaseGame {
		t.Errorf("source = %q, want %q — a costed recipe comes from the install tree, never from a mod",
			ec.Source, SourceBaseGame)
	}
	if ec.InputCost != 110 {
		t.Errorf("input cost = %v, want 110 (100 l x 1.1 — the price that was assumed to be 1.0)", ec.InputCost)
	}
	if ec.OutputValue != 122.85 {
		t.Errorf("output value = %v, want 122.85 (45 l x 2.73)", ec.OutputValue)
	}
	if ec.MarginPct == nil || *ec.MarginPct != 11.68 {
		t.Errorf("margin = %v, want 11.68%% — 23%% is the answer you get by assuming the input price", ec.MarginPct)
	}
	// 12.85 gross per cycle x 10 cycles - 2 running cost.
	if ec.NetPerHour != 126.5 {
		t.Errorf("net per hour = %v, want 126.5", ec.NetPerHour)
	}
	// The fact the expert never surfaced: at that rate a 330k factory
	// takes 2,608 in-game hours to pay for itself, which is the reason
	// not to lead a casual player's plan with it.
	if ec.PaybackHours == nil || *ec.PaybackHours != 2608.7 {
		t.Errorf("payback = %v, want 2608.7 hours", ec.PaybackHours)
	}
}

// An unpriced ingredient must drop the recipe, never be costed at zero:
// treating "the game did not say" as free turns a marginal chain into a
// spectacular one.
func TestUnpricedIngredientDropsTheRecipe(t *testing.T) {
	in := &fs25data.Install{
		FillTypes: []fs25data.FillType{{Name: "WHEAT", Title: "Wheat", PricePerLiter: 0.9}},
		Factories: []fs25data.Factory{{
			Name: "Mystery Mill",
			Recipes: []fs25data.Recipe{{
				ID:      "mystery",
				Inputs:  []fs25data.Ingredient{{FillType: "WHEAT", Amount: 100}},
				Outputs: []fs25data.Ingredient{{FillType: "UNOBTAINIUM", Amount: 10}},
				PerHour: 1,
			}},
		}},
	}
	if got := Recipes(in); len(got) != 0 {
		t.Fatalf("got %d recipes, want 0 — an unpriced output must not be costed at zero", len(got))
	}
}

// The same production point is described by several placeable files, so
// an undeduplicated ranking shows one biogas plant five times and buries
// the alternatives.
func TestDuplicateFactoryDefinitionsCollapse(t *testing.T) {
	f := fs25data.Factory{
		Name:  "Bakery",
		Price: 100000,
		Recipes: []fs25data.Recipe{{
			ID:      "bread",
			Inputs:  []fs25data.Ingredient{{FillType: "FLOUR", Amount: 100}},
			Outputs: []fs25data.Ingredient{{FillType: "BREAD", Amount: 100}},
			PerHour: 1,
		}},
	}
	in := &fs25data.Install{
		FillTypes: []fs25data.FillType{
			{Name: "FLOUR", PricePerLiter: 1},
			{Name: "BREAD", PricePerLiter: 2},
		},
		Factories: []fs25data.Factory{f, f, f},
	}
	if got := Recipes(in); len(got) != 1 {
		t.Fatalf("got %d recipes from three identical definitions, want 1", len(got))
	}
}
