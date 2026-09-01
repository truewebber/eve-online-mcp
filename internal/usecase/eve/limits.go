package eve

const (
	pagesESI        = 40
	pagesCorpAssets = 80
	pagesShort      = 10
	txLookback      = 2500
	txPages         = 4

	limitKillmails = 8
	limitShort     = 10
	limitDefault   = 15
	limitMedium    = 20
	limitLong      = 25
	limitTopItems  = 5

	decimalPlaces = 2
	roundHalf     = 0.5
	percentScale  = 100

	iskTrillion = 1e12
	iskBillion  = 1e9
	iskMillion  = 1e6
	iskThousand = 1e3

	secondsPerDay    = 86400
	secondsPerHour   = 3600
	secondsPerMinute = 60

	skillLevelV = 5

	esiSearchMinChars = 3
	hisecThreshold    = 0.45
	searchPoolFactor  = 4
	searchPoolFloor   = 50
	searchPoolMax     = 200

	fittingNameMax           = 50
	fittingDescMax           = 500
	mailRecipientsMax        = 20
	mailComposeRecipientsMax = 50
	mailSubjectMax           = 1000
	mailBodyMax              = 10000
	calendarESIPage          = 50
	cspaRecipientsMax        = 100

	typeDescPreview    = 500
	textPreview        = 300
	fittingDescPreview = 200
	routeDangerMax     = 20
	colonyStoredTop    = 8
	miningObserverCap  = 25
	overviewFetches    = 7
	corpHangarCount    = 7
	journalCategoryCap = 15

	argLimitMax    float64 = 500
	argItemsMax    float64 = 200
	argDivisionMax float64 = 7
	argHistoryDays float64 = 365
	argStandingMin float64 = -10
	argStandingMax float64 = 10

	activityManufacturing = 1
	activityResearchTE    = 3
	activityResearchME    = 4
	activityCopying       = 5
	activityReverseEng    = 7
	activityInvention     = 8
	activityReactionA     = 9
	activityReactionB     = 11
)
