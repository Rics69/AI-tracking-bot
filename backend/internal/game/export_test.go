package game

// The tests live in package game_test: they use the in-memory store, which
// imports this package.
var (
	ParseQuests    = parseQuests
	ParseChallenge = parseChallenge
	MonthStart     = monthStart
)

const ReferralXP = referralXP
