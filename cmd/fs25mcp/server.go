package main

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pequalsnp/fs25mcp/internal/plan"
)

// The tools deliberately return NUMBERS rather than prose. Every one of
// them exists because a language model asked the same question got it
// wrong in a way that read exactly like getting it right: quoting a
// recipe correctly and then dividing by a price it never looked up.
func newServer(src *source) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fs25mcp", Version: "0.1"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_farm_overview",
		Description: "The player's current farm from their most recent savegame: money, loan, " +
			"farmland owned, vehicles, map, and hours played. Start here for any question about " +
			"THEIR game rather than the game in general. Figures are as of their last save.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := src.reload(); err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(map[string]any{
			"savegame":            src.SaveName,
			"farm":                src.Save.FarmName,
			"map":                 src.Save.Map,
			"map_is_mod":          src.Save.MapIsMod,
			"money":               src.Save.Money,
			"loan":                src.Save.Loan,
			"farmland_owned":      src.Save.FarmlandOwned,
			"farmland_total":      src.Save.FarmlandTotal,
			"vehicles":            src.Save.Vehicles,
			"production_points":   len(src.Save.Productions),
			"play_time_hours":     src.Save.PlayTimeHours,
			"last_saved":          src.Save.SaveDate,
			"economic_difficulty": src.Save.EconomicDifficulty,
			"game_version":        src.Install.Version,
		})
		return res, nil, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_productions",
		Description: "Every production point the player owns, with the recipes actually switched " +
			"on, the ones switched off, and what is sitting in storage. Each one says whether it " +
			"is base game or which MOD it came from — the savegame is the only place that is " +
			"knowable, because mod content appears in no store listing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := src.reload(); err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(map[string]any{
			"savegame":    src.SaveName,
			"productions": src.Save.Productions,
		})
		return res, nil, err
	})

	type profitArgs struct {
		Query      string  `json:"query,omitempty" jsonschema:"filter by factory, recipe or ingredient name, e.g. 'rice' or 'bakery'"`
		Limit      int     `json:"limit,omitempty" jsonschema:"how many to return (default 10)"`
		Affordable bool    `json:"affordable,omitempty" jsonschema:"only production points the player can currently afford"`
		MaxPayback float64 `json:"max_payback_hours,omitempty" jsonschema:"only those that pay for themselves within this many in-game hours"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "plan_production",
		Description: "Production economics COMPUTED from the installed game's own files: input cost " +
			"and output value per cycle, gross, net per hour after running costs, margin, and how " +
			"many in-game hours the building runs before it pays for itself. Use this for any " +
			"question about what is profitable or what to build next — never do the arithmetic " +
			"yourself, and never quote a margin without an input price. Everything returned is " +
			"base-game content; mods are not in the install tree.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args profitArgs) (*mcp.CallToolResult, any, error) {
		rows := plan.Matching(src.Install, args.Query)

		if args.Affordable {
			if err := src.reload(); err != nil {
				return nil, nil, err
			}
			money := src.Save.Money
			var keep []plan.Economics
			for _, r := range rows {
				if float64(r.FactoryCost) <= money {
					keep = append(keep, r)
				}
			}
			rows = keep
		}
		if args.MaxPayback > 0 {
			var keep []plan.Economics
			for _, r := range rows {
				if r.PaybackHours != nil && *r.PaybackHours <= args.MaxPayback {
					keep = append(keep, r)
				}
			}
			rows = keep
		}

		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}
		if len(rows) > limit {
			rows = rows[:limit]
		}

		res, err := jsonResult(map[string]any{
			"game_version": src.Install.Version,
			"note": "Prices are base price per litre, before a selling point's own multiplier " +
				"and before the month factor.",
			"recipes": rows,
		})
		return res, nil, err
	})

	type stockArgs struct {
		Limit int `json:"limit,omitempty" jsonschema:"how many fill types to return (default 20)"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_storage",
		Description: "What the player is actually sitting on across every production point, valued " +
			"at base price. Answers 'what should I sell' and 'why is my chain stalled' — a full " +
			"output buffer stops production, and an empty input buffer means the chain is idle.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args stockArgs) (*mcp.CallToolResult, any, error) {
		if err := src.reload(); err != nil {
			return nil, nil, err
		}
		econ := plan.NewEconomy(src.Install)

		type stock struct {
			FillType string  `json:"fill_type"`
			Litres   float64 `json:"litres"`
			Value    float64 `json:"value_at_base_price,omitempty"`
			Priced   bool    `json:"priced"`
		}
		totals := map[string]float64{}
		for _, p := range src.Save.Productions {
			for ft, l := range p.Storage {
				totals[ft] += l
			}
		}
		out := make([]stock, 0, len(totals))
		for ft, l := range totals {
			st := stock{FillType: ft, Litres: round(l)}
			if price, ok := econ.Price(ft); ok {
				st.Value = round(price * l)
				st.Priced = true
			}
			out = append(out, st)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })

		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		if len(out) > limit {
			out = out[:limit]
		}
		res, err := jsonResult(map[string]any{"savegame": src.SaveName, "storage": out})
		return res, nil, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "refresh_save",
		Description: "Re-read the savegame from disk. The player's state is only as fresh as their " +
			"last in-game save, so call this after they say they have saved — and tell them the " +
			"figures are from that save, not from live play.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := src.reload(); err != nil {
			return nil, nil, err
		}
		res, err := jsonResult(map[string]any{
			"savegame":   src.SaveName,
			"last_saved": src.Save.SaveDate,
			"money":      src.Save.Money,
			"play_time":  src.Save.PlayTimeHours,
		})
		return res, nil, err
	})

	return s
}

func round(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
