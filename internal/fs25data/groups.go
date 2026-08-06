// Package fs25data parses a local Farming Simulator 25 installation: the
// shop catalogue, crops, fill types and production chains, straight from
// the game's own XML.
//
// It lives in the public MCP server rather than in the private knowledge
// base because both need it and the dependency can only point one way.
package fs25data

import "strings"

// categoryRules maps a store category onto a group by substring, first
// match wins. Order carries the meaning: "forestryHarvesters" must reach
// forestry before "harvester" claims it, and "loaderWagons" must reach
// forage before "loader" claims it. A rules list rather than an exhaustive
// map because the game adds categories every patch and an unknown
// category should land somewhere sensible rather than vanish.
var categoryRules = []struct {
	match string
	group string
}{
	{"forestry", "forestry"},
	{"woodchipper", "forestry"},
	{"forwarder", "forestry"},
	{"winch", "forestry"},
	{"loaderwagon", "forage"},
	{"baler", "forage"},
	{"baling", "forage"},
	{"bale", "forage"},
	{"wrapper", "forage"},
	{"mower", "forage"},
	{"tedder", "forage"},
	{"windrower", "forage"},
	{"foragemixer", "forage"},
	{"silocompaction", "forage"},
	{"strawblower", "forage"},
	{"tractor", "tractors"},
	{"harvester", "harvesting"},
	{"harvesting", "harvesting"},
	{"header", "harvesting"},
	{"cutter", "harvesting"},
	{"beetloading", "harvesting"},
	{"planter", "planting"},
	{"planting", "planting"},
	{"seeder", "planting"},
	{"sowing", "planting"},
	{"seedtank", "planting"},
	{"sprayer", "fertilizing"},
	{"fertilizer", "fertilizing"},
	{"manure", "fertilizing"},
	{"slurry", "fertilizing"},
	{"spreader", "fertilizing"},
	{"barrel", "fertilizing"},
	{"cultivator", "tillage"},
	{"harrow", "tillage"},
	{"plow", "tillage"},
	{"subsoiler", "tillage"},
	{"spader", "tillage"},
	{"roller", "tillage"},
	{"weeder", "tillage"},
	{"mulcher", "tillage"},
	{"stonepicker", "tillage"},
	{"leveler", "tillage"},
	{"grasslandcare", "tillage"},
	{"lowloader", "transport"},
	{"trailer", "transport"},
	{"tipper", "transport"},
	{"augerwagon", "transport"},
	{"transport", "transport"},
	{"belt", "transport"},
	{"truck", "transport"},
	{"telehandler", "loaders"},
	{"loader", "loaders"},
	{"forklift", "loaders"},
	{"skidsteer", "loaders"},
	{"crane", "loaders"},
}

// GroupFor buckets a store item's categories. Items can carry several
// ("lowloaders trailersSemi"), and the rules are scanned outermost so the
// documented priority actually holds: whichever rule ranks highest wins,
// no matter which category it matched. Scanning categories first would
// make the answer depend on the order the game happened to write the
// attribute — "trailers loaderWagons" and "loaderWagons trailers" would
// land in different documents. Anything unrecognised becomes "misc", so a
// category added by a future patch still shows up somewhere.
func GroupFor(categories []string) string {
	lowered := make([]string, 0, len(categories))
	for _, c := range categories {
		lowered = append(lowered, strings.ToLower(c))
	}
	for _, rule := range categoryRules {
		for _, c := range lowered {
			if strings.Contains(c, rule.match) {
				return rule.group
			}
		}
	}
	return "misc"
}
