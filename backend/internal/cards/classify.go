package cards

import (
	"regexp"
	"strings"
)

type Classification struct {
	Category  Category `json:"category"`
	Confident bool     `json:"confident"` // false => UI should ask the user to confirm
}

type classRule struct {
	re  *regexp.Regexp
	cat Category
}

var (
	noiseWords = regexp.MustCompile(`\b(UPI|POS|ECOM|IMPS|NEFT|PUR|TXN|REF|P2M|PAYMENT|PVT|LTD)\b`)
	nonAlpha   = regexp.MustCompile(`[^A-Z ]+`)
	classRules = compileRules([]struct {
		p string
		c Category
	}{
		// Order matters: first match wins. EXCLUDED is first so "CRED RENT" never lands in Utilities
		// and "AMAZON PAY" (wallet load) never lands in Online_Shopping.
		{`\b(CRED|RENT|NOBROKER|RENTOMOJO|LIC|INSURANCE|GST|INCOME TAX|CHALLAN|WALLET|AMAZON PAY|PAYTM WALLET|MOBIKWIK|EMI)\b`, Excluded},
		{`\b(INSTAMART|BLINKIT|ZEPTO|BIGBASKET|DMART|JIOMART|GROFERS|RELIANCE FRESH)\b`, Groceries},
		{`\b(SWIGGY|ZOMATO|DOMINO|DOMINOS|MCDONALD|MCDONALDS|KFC|STARBUCKS|BURGER KING|BARBEQUE|EATSURE|DINEOUT)\b`, Dining},
		{`\b(INDIGO|AIR INDIA|VISTARA|AKASA|SPICEJET|MAKEMYTRIP|MMT|GOIBIBO|CLEARTRIP|AGODA|TAJ|OYO)\b`, FlightsHotels},
		{`\b(IOCL|INDIAN OIL|BPCL|HPCL|SHELL|NAYARA|PETROL|FUEL)\b`, Fuel},
		{`\b(BESCOM|TATA POWER|ADANI ELECTRICITY|BSES|MSEDCL|AIRTEL|JIO|VODAFONE|ACT FIBERNET|WATER BOARD)\b`, Utilities},
		{`\b(AMAZON|FLIPKART|MYNTRA|AJIO|NYKAA|MEESHO|TATA CLIQ|BOOKMYSHOW)\b`, OnlineShopping},
	})
)

func compileRules(src []struct {
	p string
	c Category
}) []classRule {
	out := make([]classRule, len(src))
	for i, s := range src {
		out[i] = classRule{regexp.MustCompile(s.p), s.c}
	}
	return out
}

// Classify buckets a messy bank/UPI transaction description. Unknown merchants default to Offline_POS,
// marked confident only when the string says it came through a POS terminal.
func Classify(raw string) Classification {
	up := strings.ToUpper(raw)
	isPOS := strings.HasPrefix(up, "POS") || strings.Contains(up, "POS ")
	norm := strings.Join(strings.Fields(nonAlpha.ReplaceAllString(noiseWords.ReplaceAllString(up, " "), " ")), " ")
	for _, r := range classRules {
		if r.re.MatchString(norm) {
			return Classification{r.cat, true}
		}
	}
	return Classification{OfflinePOS, isPOS}
}
