package tracker

// countryBlocks are ICAO 24-bit address allocations (ICAO Annex 10 Vol III, Part I, Ch. 9) for Israel,
// its neighbours, Europe and the most common long-haul states. Not exhaustive.
var countryBlocks = []struct {
	from, to uint32
	country  string
}{
	{0x010000, 0x017FFF, "Egypt"},
	{0x06A000, 0x06A3FF, "Qatar"},
	{0x100000, 0x1FFFFF, "Russia"},
	{0x300000, 0x33FFFF, "Italy"},
	{0x340000, 0x37FFFF, "Spain"},
	{0x380000, 0x3BFFFF, "France"},
	{0x3C0000, 0x3FFFFF, "Germany"},
	{0x400000, 0x43FFFF, "United Kingdom"},
	{0x440000, 0x447FFF, "Austria"},
	{0x448000, 0x44FFFF, "Belgium"},
	{0x450000, 0x457FFF, "Bulgaria"},
	{0x458000, 0x45FFFF, "Denmark"},
	{0x460000, 0x467FFF, "Finland"},
	{0x468000, 0x46FFFF, "Greece"},
	{0x470000, 0x477FFF, "Hungary"},
	{0x478000, 0x47FFFF, "Norway"},
	{0x480000, 0x487FFF, "Netherlands"},
	{0x488000, 0x48FFFF, "Poland"},
	{0x490000, 0x497FFF, "Portugal"},
	{0x498000, 0x49FFFF, "Czech Republic"},
	{0x4A0000, 0x4A7FFF, "Romania"},
	{0x4A8000, 0x4AFFFF, "Sweden"},
	{0x4B0000, 0x4B7FFF, "Switzerland"},
	{0x4B8000, 0x4BFFFF, "Turkey"},
	{0x4C0000, 0x4C7FFF, "Serbia"},
	{0x4C8000, 0x4C83FF, "Cyprus"},
	{0x4CA000, 0x4CAFFF, "Ireland"},
	{0x4D2000, 0x4D23FF, "Malta"},
	{0x502C00, 0x502FFF, "Latvia"},
	{0x503C00, 0x503FFF, "Lithuania"},
	{0x508000, 0x50FFFF, "Ukraine"},
	{0x511000, 0x5113FF, "Estonia"},
	{0x514000, 0x5143FF, "Georgia"},
	{0x600000, 0x6003FF, "Armenia"},
	{0x600800, 0x600BFF, "Azerbaijan"},
	{0x683000, 0x6833FF, "Kazakhstan"},
	{0x710000, 0x717FFF, "Saudi Arabia"},
	{0x738000, 0x73FFFF, "Israel"},
	{0x740000, 0x747FFF, "Jordan"},
	{0x896000, 0x896FFF, "United Arab Emirates"},
	{0xA00000, 0xAFFFFF, "United States"},
}

// Country returns the state an ICAO address block is allocated to, or "" when not in the table.
func Country(icao uint32) string {
	for _, b := range countryBlocks {
		if icao >= b.from && icao <= b.to {
			return b.country
		}
	}
	return ""
}
