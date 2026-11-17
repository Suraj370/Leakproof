package mf

// MaxLookThroughDepth bounds fund-of-funds nesting. Real structures are one level (a FoF holding funds);
// three covers feeder-of-FoF cases and also stops runaway data.
const MaxLookThroughDepth = 3

// LookThrough replaces every holding that is itself a fund in `funds` with that fund's own holdings,
// scaled by the weight held (weights are bps: child = parent * underlying / 10000, rounded down).
// The input map is not modified. Cycles (A holds B holds A) and nesting beyond MaxLookThroughDepth leave
// the inner fund as an opaque holding rather than looping. Flat funds come back unchanged, and structures within
// MaxLookThroughDepth are fully expanded (so running it again on its own output changes nothing); deeper chains
// keep an opaque tail that a second pass would expand further, so apply it exactly once, at load time.
func LookThrough(funds map[string]Fund) map[string]Fund {
	out := make(map[string]Fund, len(funds))
	for isin, f := range funds {
		h, names := flatten(funds, f, 1, map[string]bool{isin: true})
		f.Holdings, f.HoldingNames = h, names
		out[isin] = f
	}
	return out
}

func flatten(funds map[string]Fund, f Fund, depth int, path map[string]bool) (map[string]int64, map[string]string) {
	h := make(map[string]int64, len(f.Holdings))
	names := make(map[string]string, len(f.Holdings))
	for stock, w := range f.Holdings {
		under, isFund := funds[stock]
		if !isFund || path[stock] || depth > MaxLookThroughDepth {
			h[stock] += w
			if n := f.HoldingNames[stock]; n != "" {
				names[stock] = n
			}
			continue
		}
		path[stock] = true
		sub, subNames := flatten(funds, under, depth+1, path)
		delete(path, stock)
		for s, sw := range sub {
			h[s] += w * sw / 10_000
			if n := subNames[s]; n != "" {
				names[s] = n
			}
		}
	}
	return h, names
}
