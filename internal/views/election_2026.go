package views

import "thundercitizen/internal/models"

const (
	Election2026OfficialURL          = "https://www.thunderbay.ca/en/city-hall/2026-municipal-election.aspx"
	Election2026CandidateProfilesURL = "https://www.thunderbay.ca/en/city-hall/2026-election-candidate-profiles.aspx"
)

// ElectionPageKind describes what a candidate-owned link actually is. Keeping
// this closed list prevents an older or professional page from being presented
// as a current campaign website.
type ElectionPageKind string

const (
	ElectionPageCampaign     ElectionPageKind = "Campaign site"
	ElectionPageCandidate    ElectionPageKind = "Candidate site"
	ElectionPageProfessional ElectionPageKind = "Professional page"
)

// ElectionCandidatePage is a candidate-owned web presence.
type ElectionCandidatePage struct {
	URL   string
	Kind  ElectionPageKind
	Label string
}

func (p ElectionCandidatePage) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return string(p.Kind)
}

// ElectionCandidateSocial is a public account that a candidate either linked
// from their campaign site or supplied through the City Clerk profile. It is
// deliberately separate from a candidate's web page so professional accounts
// and stale campaign accounts are not presented as current campaign channels.
type ElectionCandidateSocial struct {
	Platform string
	URL      string
	Label    string
}

func (s ElectionCandidateSocial) DisplayLabel() string {
	if s.Label != "" {
		return s.Label
	}
	return s.Platform + " profile"
}

// ElectionCandidateView is the same neutral card model for every candidate.
// SortName makes the disclosed surname-order rule explicit and testable.
type ElectionCandidateView struct {
	Name         string
	SortName     string
	Summary      string
	OfficeStatus string
	Page         *ElectionCandidatePage
	Socials      []ElectionCandidateSocial
	Sources      []models.SourceRef
}

// ElectionContestView represents one choice on a voter's ballot.
type ElectionContestView struct {
	ID         string
	Name       string
	Seats      int
	Choice     string
	Intro      string
	Acclaimed  bool
	Candidates []ElectionCandidateView
}

// Election2026ViewModel contains the certified municipal slate and the
// currently published trustee slate. It is static, compiled data: no database,
// JavaScript, or campaign-controlled feed is involved.
type Election2026ViewModel struct {
	OfficialSource   models.SourceRef
	ElectionDayDate  string
	ElectionDayHours string
	AdvanceVoteDates string
	AdvanceVoteHours string
	Mayor            ElectionContestView
	AtLarge          ElectionContestView
	Wards            []ElectionContestView
	Trustees         []ElectionContestView
}

func electionPage(url string, kind ElectionPageKind) *ElectionCandidatePage {
	return &ElectionCandidatePage{URL: url, Kind: kind}
}

func electionLabeledPage(url string, kind ElectionPageKind, label string) *ElectionCandidatePage {
	p := electionPage(url, kind)
	p.Label = label
	return p
}

func electionSource(url, label string) models.SourceRef {
	return models.SourceRef{URL: url, Label: label}
}

func electionCandidate(name, sortName, summary, status string, page *ElectionCandidatePage, sources ...models.SourceRef) ElectionCandidateView {
	return ElectionCandidateView{
		Name:         name,
		SortName:     sortName,
		Summary:      summary,
		OfficeStatus: status,
		Page:         page,
		Socials:      electionCandidateSocials[name],
		Sources:      sources,
	}
}

// These profiles and campaign announcements were checked August 28, 2026.
// Each is linked by the named candidate's current campaign/personal site,
// supplied through the City Clerk profile, or is a named campaign announcement.
// Older campaign accounts are intentionally omitted until their current use is
// confirmed.
var electionCandidateSocials = map[string][]ElectionCandidateSocial{
	"Trevor Giertuga": {
		{Platform: "Facebook", URL: "https://www.facebook.com/trevor.giertuga"},
	},
	"Peter Panetta": {
		{Platform: "Facebook", URL: "https://www.facebook.com/peter.panetta.2025"},
	},
	"Rajni Agarwal": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/1G5LLyJNCy/", Label: "Facebook campaign announcement"},
	},
	"Julie Colquhoun": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/1JvRof1YQx/", Label: "Facebook campaign announcement"},
		{Platform: "Facebook", URL: "https://www.facebook.com/julie.colquhoun.3?__cft__[0]=AZYC8hVfD4V2wHhIzn9zoUH81aW5ElsfIQjqx_OFlMSomWQ3gvQZk2pdQpuuZ-iTjU2Ea9dKuLEWX4ijOgTzBPU95p0RjHa-yztGxBqLUZHBQzqhb-3CzExSEh_M7LBONWhvYZEO28dzX2sID7AjB7iJ&__tn__=-UC%2CP-R"},
	},
	"Kasey Taylor Etreni": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/195on9DoE9/", Label: "Facebook campaign announcement"},
	},
	"Peng You": {
		{Platform: "Facebook", URL: "https://www.facebook.com/pengyou.peng.37"},
	},
	"Dino Cicchitano": {
		{Platform: "Facebook", URL: "https://www.facebook.com/dcicchitano"},
	},
	"John Murray": {
		{Platform: "Facebook", URL: "https://www.facebook.com/profile.php?id=61593615248445"},
	},
	"Greg Johnsen": {
		{Platform: "Facebook", URL: "https://www.facebook.com/onsen.johnsen"},
	},
	"Tony DiPaolo": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/1FhQfeVbeg/", Label: "Facebook campaign announcement"},
	},
	"Shane Judge": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/14g14ZYoAeU/", Label: "Facebook campaign announcement"},
	},
	"Mark Bentz": {
		{Platform: "Facebook", URL: "https://www.facebook.com/mark.bentz.790/posts/pfbid0hpX4gmGwX5KQ7qudhprPYKCkucybnpePSamGiQ8HFpFwoLTwRWzhh2BtXSbFoTMMl", Label: "Facebook campaign announcement"},
	},
	"Gene Capar": {
		{Platform: "Facebook", URL: "https://www.facebook.com/groups/1782214149442200/posts/1784966399166975/", Label: "Facebook campaign announcement"},
	},
	"Gary Christian": {
		{Platform: "Facebook", URL: "https://www.facebook.com/permalink.php?story_fbid=pfbid0UngBjLPr7bw2CJQtmVzUDW48FKPdrfMcwCnVqWrZkQcTArgNikGFY4Lq7YGvLenvl&id=61592912046098", Label: "Facebook campaign announcement"},
	},
	"Tyler Goode": {
		{Platform: "Facebook", URL: "https://www.facebook.com/permalink.php?story_fbid=pfbid0Gto77r6RmFD1wGZ8D7ibhhUPc66Fus9fSzoHsvy4EUv7SyyY6KznewcdDtFJWiejl&id=61593323288022", Label: "Facebook campaign announcement"},
	},
	"Jamie Nichols": {
		{Platform: "Facebook", URL: "https://www.facebook.com/rose.crantz/posts/pfbid0wsfRwUcksN61k4x3Xco4tBNNaquQJmPD54Bm7ThCswC5QvGysCWzba1xfxj97Fe3l", Label: "Facebook campaign announcement"},
	},
	"Cory Bagdon": {
		{Platform: "Facebook", URL: "https://www.facebook.com/permalink.php?story_fbid=pfbid0tYUFkxVSatNi8Ui4hexPRnn7odYK5US9UqS4zgWHHRqgni8p6b3CR3QHkZKyWJt1l&id=100084445181438", Label: "Facebook campaign announcement"},
	},
	"Maureen (Moe) Comuzzi": {
		{Platform: "Facebook", URL: "https://www.facebook.com/profile.php?id=61590839212992"},
		{Platform: "Instagram", URL: "https://www.instagram.com/moecomuzziformayor/"},
	},
	"Peter Diedrich": {
		{Platform: "Facebook", URL: "https://www.facebook.com/profile.php?id=61589436061520"},
		{Platform: "Instagram", URL: "https://www.instagram.com/peterdiedrich4mayor/"},
	},
	"Volker Kromm": {
		{Platform: "Facebook", URL: "https://www.facebook.com/profile.php?id=61574431809816"},
		{Platform: "Instagram", URL: "https://www.instagram.com/volkerkromm/"},
	},
	"Wolfgang Schoor": {
		{Platform: "Facebook", URL: "https://www.facebook.com/WolfgangErgonomosSchoor"},
	},
	"Aldo Ruberto": {
		{Platform: "Facebook", URL: "https://www.facebook.com/Rubertoaldo"},
	},
	"Syed Kabir": {
		{Platform: "Facebook", URL: "https://www.facebook.com/SKabirTbay"},
	},
	"John Ongaro": {
		{Platform: "Facebook", URL: "https://www.facebook.com/profile.php?id=61593340404509"},
		{Platform: "Instagram", URL: "https://www.instagram.com/johnongaro_official/"},
	},
	"Stephen Margarit": {
		{Platform: "Facebook", URL: "https://www.facebook.com/votemargarit"},
		{Platform: "Instagram", URL: "https://www.instagram.com/votemargarit/"},
	},
	"André Gagné": {
		{Platform: "Facebook", URL: "https://www.facebook.com/groups/28272928153/user/61589073457212"},
	},
	"Dino Menei": {
		{Platform: "Facebook", URL: "https://www.facebook.com/dino.menei"},
	},
	"Jamie Scrimger": {
		{Platform: "Facebook", URL: "https://www.facebook.com/jamie.scrimger.7"},
	},
	"Brian Hamilton": {
		{Platform: "Facebook", URL: "https://www.facebook.com/share/p/19PPgVuVik/", Label: "Facebook campaign announcement"},
	},
}

// NewElection2026ViewModel returns the certified 2026 roster published by the
// City of Thunder Bay.
func NewElection2026ViewModel() Election2026ViewModel {
	mayor := ElectionContestView{
		ID:     "mayor-candidates",
		Name:   "Mayor",
		Seats:  1,
		Choice: "Choose 1",
		Intro:  "",
		Candidates: []ElectionCandidateView{
			electionCandidate("Maureen (Moe) Comuzzi", "Comuzzi, Maureen", "A Thunder Bay business and real-estate professional who launched her mayoral campaign in August.", "", electionPage("https://moeformayor.ca/", ElectionPageCampaign), electionSource("https://acadiabroadcasting.ca/the-mayors-race-begins-moe-comuzzi-announces-her-bid/", "Acadia News campaign launch")),
			electionCandidate("Peter Diedrich", "Diedrich, Peter", "An engineer, venture-capital executive and former Tbaytel chief executive.", "", electionPage("https://peterdiedrich4mayor.com/", ElectionPageCampaign), electionSource("https://yourthunderbay.ca/thunder-bay-elections-our-interview-with-peter-diedrich/", "Your Thunder Bay interview")),
			electionCandidate("Trevor Giertuga", "Giertuga, Trevor", "A current at-large councillor, first elected to council in 2000.", "At-Large Councillor", electionPage("https://www.trevor4mayor.ca/", ElectionPageCampaign)),
			electionCandidate("Shane Judge", "Judge, Shane", "A retired journalist making a second run for mayor after campaigning in 2022.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/judge-to-take-second-run-at-mayors-chair-12633625", "TBNewsWatch candidate profile")),
			electionCandidate("Volker Kromm", "Kromm, Volker", "Long-time executive director of the Regional Food Distribution Association.", "", electionPage("https://www.votevolker.ca/", ElectionPageCampaign), electionSource("https://foodbankscanada.ca/food-banker-spotlight-volker-kromm-of-the-regional-food-distribution-association/", "Food Banks Canada profile")),
			electionCandidate("Peter Panetta", "Panetta, Peter", "Founder of the Underground Gym, where he has worked with youth through boxing and mentorship, and a previous mayoral candidate.", "", electionLabeledPage("https://undergroundgym.ca/", ElectionPageProfessional, "Underground Gym"), electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/panetta-to-challenge-for-another-shot-at-mayor-12668353", "TBNewsWatch candidate profile")),
			electionCandidate("Aldo Ruberto", "Ruberto, Aldo", "A former four-term at-large councillor who served from 2006 to 2022.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/aldo-ruberto-enters-crowded-mayoral-race-12649095", "TBNewsWatch candidate profile")),
			electionCandidate("Wolfgang Schoor", "Schoor, Wolfgang", "His personal site describes work in construction and ergonomics; he has previously sought municipal office.", "", electionPage("https://wolfgangschoor.com/wolfgang-schoor-for-mayor-2026/", ElectionPageCandidate)),
			electionCandidate("Doug Vincent", "Vincent, Doug", "A former City licensing manager with 40 years of public-service experience.", "", nil, electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/doug-vincent-brings-40-years-of-public-service-to-mayoral-race-12697102", "TBNewsWatch candidate profile")),
			electionCandidate("Donald Baxter", "Baxter, Donald", "A Caring Hearts Cat Rescue & Sanctuary volunteer who rescues, feeds and fosters animals.", "", nil, electionSource("https://www.tbnewswatch.com/holiday-heroes/2018-nominees/holiday-hero-nominee-donald-baxter-2-photos-1144994", "TBNewsWatch volunteer profile")),
		},
	}

	atLarge := ElectionContestView{
		ID:     "at-large-candidates",
		Name:   "At-Large Councillor",
		Seats:  5,
		Choice: "Choose up to 5",
		Intro:  "All voters. Incumbents are marked.",
		Candidates: []ElectionCandidateView{
			electionCandidate("Rajni Agarwal", "Agarwal, Rajni", "A current at-large councillor and local business owner.", "At-Large Councillor", nil, electionSource("https://www.tbnewswatch.com/local-news/rajni-agarwal-running-to-keep-at-large-council-seat-12688979", "TBNewsWatch candidate profile")),
			electionCandidate("Mark Bentz", "Bentz, Mark", "A current at-large councillor who has also served as Northwood councillor and a school trustee.", "At-Large Councillor", nil),
			electionCandidate("Gene Capar", "Capar, Gene", "A Thunder Bay tabletop-game creator who launched two crowdfunded fantasy-miniatures projects.", "", nil),
			electionCandidate("Gary Christian", "Christian, Gary", "Executive director of the North Superior Workforce Planning Board and an appointed Lakehead trustee since June 2026.", "School Trustee", nil, electionSource("https://www.lakeheadschools.ca/general/the-lakehead-district-school-board-is-pleased-to-welcome-gary-christian-as-trustee/", "Lakehead trustee appointment")),
			electionCandidate("Julie Colquhoun", "Colquhoun, Julie", "An at-large candidate calling for a collaborative, fiscally responsible approach to safety, housing, local business and economic development.", "", nil),
			electionCandidate("Patrick George Cully", "Cully, Patrick George", "An accessibility and inclusion advocate campaigning to revive Thunder Bay's \"Giant Heart\" identity.", "", nil, electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/patrick-cully-enters-at-large-council-race-12687228", "TBNewsWatch candidate profile")),
			electionCandidate("Heather K. Dahlstrom", "Dahlstrom, Heather K.", "A Thunder Bay-born film producer whose work has screened at Sundance and TIFF.", "", nil),
			electionCandidate("Stephanie Danylko", "Danylko, Stephanie", "A 2022 McKellar Ward candidate with past workplace service in union and human-rights roles.", "", nil),
			electionCandidate("Kasey Taylor Etreni", "Etreni, Kasey Taylor", "A current at-large councillor and retired radiation therapist.", "At-Large Councillor", nil),
			electionCandidate("Tyler Goode", "Goode, Tyler", "A registered social worker whose work has included cultural mental-health programming with Matawa.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/tyler-goode-enters-at-large-council-race-12692380", "TBNewsWatch candidate profile")),
			electionCandidate("Dino Menei", "Menei, Dino", "A recycling and heavy-equipment contractor, hobby farmer and previous at-large candidate.", "", nil),
			electionCandidate("Jamie Nichols", "Nichols, Jamie", "Founder of RNC Coffee and a Thunder Bay Chamber of Commerce board member.", "", electionLabeledPage("https://rnccoffee.ca/", ElectionPageProfessional, "RNC Coffee"), electionSource("https://www.tbnewswatch.com/local-news/jaime-nichols-brings-business-experience-to-at-large-race-12646033", "TBNewsWatch candidate profile")),
			electionCandidate("Robert Trevisan", "Trevisan, Robert", "A Thunder Bay chiropractor and Lakehead University alumnus.", "", nil),
			electionCandidate("Peng You", "You, Peng", "A former at-large councillor who served during the 2018–2022 term.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/former-councillor-peng-you-seeks-at-large-seat-12646078", "TBNewsWatch candidate profile")),
		},
	}

	wards := []ElectionContestView{
		{
			ID: "ward-current-river", Name: "Current River", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("Andrew Foulds", "Foulds, Andrew", "A teacher and fifth-term Current River councillor.", "Ward Councillor", nil),
				electionCandidate("Stéphane Léonard Kuziora", "Kuziora, Stéphane Léonard", "A Current River candidate who returned to Thunder Bay after studying in Norway and advocates evidence-informed, cost-effective action on homelessness and municipal priorities.", "", nil),
			},
		},
		{
			ID: "ward-red-river", Name: "Red River", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("Cory Bagdon", "Bagdon, Cory", "A teacher, furniture maker and previous council candidate who has served on the library board.", "", electionLabeledPage("https://pawoodcraft.ca/", ElectionPageProfessional, "Pine + Alder Woodcraft")),
				electionCandidate("Dino Cicchitano", "Cicchitano, Dino", "A local financial-services professional and business owner.", "", nil, electionSource("https://vote.chroniclejournal.com/the-co-operators", "Chronicle-Journal business profile"), electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/dino-cicchitano-runs-for-red-river-ward-seat-12693448", "TBNewsWatch candidate profile")),
				electionCandidate("Michael Giardetti", "Giardetti, Michael", "A supply-chain and procurement professional with teaching experience at Confederation College.", "", nil),
				electionCandidate("John Murray", "Murray, John", "Founder of Red Lion Smokehouse and a Shelter House board member.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/john-murray-enters-red-river-ward-race-12692728", "TBNewsWatch candidate profile")),
				electionCandidate("Jamie Scrimger", "Scrimger, Jamie", "An energy-sector professional with previous leadership involvement in the First Nations Emergency Response Association.", "", nil, electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/jamie-scrimger-enters-red-river-ward-council-race-12696014", "TBNewsWatch candidate profile")),
			},
		},
		{
			ID: "ward-mcintyre", Name: "McIntyre", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("Albert Aiello", "Aiello, Albert", "The current McIntyre councillor and executive director of BGC Thunder Bay.", "Ward Councillor", electionPage("https://www.albertaiello.com/", ElectionPageCandidate)),
				electionCandidate("Brian Phillips", "Phillips, Brian", "Manager of Arthur Street Medical Health Centre (Spence Clinic) and a previous at-large council candidate.", "", nil, electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/brian-phillips-enters-race-for-mcintyre-ward-seat-12693575", "TBNewsWatch candidate profile")),
			},
		},
		{
			ID: "ward-mckellar", Name: "McKellar", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("Marco Cupelli", "Cupelli, Marco", "A local property manager who has written publicly about municipal leadership and housing.", "", nil, electionSource("https://www.thewalleye.ca/stories/thunder-bay-doesnt-need-another-politicianit-needs-leadership", "The Walleye civic-leadership essay"), electionSource("https://www.tbnewswatch.com/municipal-election/2026-municipal-election/third-candidate-files-to-run-in-mckellar-12687123", "TBNewsWatch candidate profile")),
				electionCandidate("Tony DiPaolo", "DiPaolo, Tony", "Chief executive of the Thunder Bay Border Cats and a former business-improvement-area chair.", "", nil, electionSource("https://northwoodsleague.com/thunder-bay-border-cats/2019/03/09/ceo/", "Thunder Bay Border Cats announcement")),
				electionCandidate("Brian Hamilton", "Hamilton, Brian", "The current McKellar councillor, first elected in 2018, and a small-business owner.", "Ward Councillor", nil),
				electionCandidate("Tracey MacKinnon", "MacKinnon, Tracey", "An Indigenous community advocate whose public work has focused on poverty and housing.", "", nil, electionSource("https://www.ola.org/sites/default/files/node-files/hansard/document/pdf/2026/2026-02/28-JAN-2026_F018.pdf", "Ontario Legislature testimony")),
				electionCandidate("Donna Lee Morettin", "Morettin, Donna Lee", "A hospitality-sector business owner and former Chamber and business-improvement-area leader.", "", nil),
			},
		},
		{
			ID: "ward-northwood", Name: "Northwood", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("André Gagné", "Gagné, André", "A logistics and business-development professional involved in local construction projects.", "", nil),
				electionCandidate("Syed Kabir", "Kabir, Syed", "His campaign site describes a background in business, media and community organizations.", "", electionPage("https://syedkabir.ca/", ElectionPageCampaign)),
				electionCandidate("John Ongaro", "Ongaro, John", "A long-time local broadcaster and programming executive.", "", electionPage("https://www.johnongaro.ca/", ElectionPageCampaign)),
			},
		},
		{
			ID: "ward-westfort", Name: "Westfort", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("Angel Gamble", "Gamble, Angel", "A First Nations business owner in Westfort and first-time municipal candidate with prior union political-action experience in Alberta.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/candidate-for-westfort-ward-wants-to-bring-people-together-12710446", "TBNewsWatch candidate profile")),
				electionCandidate("Clinton Harris", "Harris, Clinton", "A former publisher and teacher who has served on community boards and ran for mayor in 2022.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/harris-shifts-from-mayoral-race-to-ward-councillor-bid-12689527", "TBNewsWatch candidate profile")),
				electionCandidate("Stephen Margarit", "Margarit, Stephen", "A community and political-party organizer and Rotary club leader who has previously sought elected office.", "", nil, electionSource("https://fwrotary.ca/clubexecutives", "Fort William Rotary executive")),
			},
		},
		{
			ID: "ward-neebing", Name: "Neebing", Seats: 1, Choice: "Choose 1",
			Candidates: []ElectionCandidateView{
				electionCandidate("John Warren Beals", "Beals, John Warren", "A former local business owner who operated the Neebing Roadhouse and Best Western Plus Nor'Wester Hotel and Conference Centre.", "", nil, electionSource("https://www.tbnewswatch.com/local-news/john-beals-enters-race-for-neebing-ward-12693862", "TBNewsWatch candidate profile")),
				electionCandidate("Greg Johnsen", "Johnsen, Greg", "The current Neebing councillor, with a professional background in education and history.", "Ward Councillor", nil),
			},
		},
	}

	trustees := []ElectionContestView{
		{
			ID: "trustee-english-public", Name: "English Public School Board Trustee", Seats: 8, Choice: "Choose up to 8",
			Intro: "For English Public voters.",
			Candidates: []ElectionCandidateView{
				electionCandidate("Susan Frattaroli", "Frattaroli, Susan", "A human-resources and payroll professional with community-board experience.", "", nil),
				electionCandidate("CJ Goater", "Goater, CJ", "A local broadcaster and journalist.", "", nil),
				electionCandidate("Kathleen Heikkinen", "Heikkinen, Kathleen", "No verified public biography has been added from the sources reviewed.", "", nil),
				electionCandidate("Patricia Johansen", "Johansen, Patricia", "A current Lakehead Public Schools trustee.", "Trustee", nil),
				electionCandidate("Scotia-Leigh Kauppi", "Kauppi, Scotia-Leigh", "A small-business owner and community volunteer.", "", nil),
				electionCandidate("Lex MacArthur", "MacArthur, Lex", "An accountant and community volunteer campaigning on four published education priorities.", "", nil, electionSource("https://www.netnewsledger.com/2026/08/22/lex-macarthur-enters-the-2026-lakehead-school-trustee-race-with-a-four-pillar-platform/", "NetNewsLedger candidate profile")),
				electionCandidate("Deborah Massaro", "Massaro, Deborah", "A former Lakehead trustee and board chair.", "", nil, electionSource("https://www.lakeheadschools.ca/wp-content/uploads/2021/12/2016-09-27-Regular-Board-Meeting-No-15.pdf", "Lakehead board minutes")),
				electionCandidate("Ian Morgan", "Morgan, Ian", "A scientist with a doctoral degree and chemical-industry experience.", "", nil),
				electionCandidate("Vish Nayyar", "Nayyar, Vish", "No verified public biography has been added from the sources reviewed.", "", nil),
				electionCandidate("George Saarinen", "Saarinen, George", "A long-serving Lakehead Public Schools trustee.", "Trustee", nil),
				electionCandidate("Ryan Sitch", "Sitch, Ryan", "A current Lakehead Public Schools trustee.", "Trustee", nil),
				electionCandidate("Trudy Tuchenhagen", "Tuchenhagen, Trudy", "A current Lakehead Public Schools trustee.", "Trustee", nil),
			},
		},
		{
			ID: "trustee-english-separate", Name: "English Separate School Board Trustee", Seats: 6, Choice: "Choose up to 6",
			Intro: "For English Separate voters.",
			Candidates: []ElectionCandidateView{
				electionCandidate("Eleanor Ashe", "Ashe, Eleanor", "A current Thunder Bay Catholic trustee.", "Trustee", nil),
				electionCandidate("Lawrence Badanai", "Badanai, Lawrence", "A current Thunder Bay Catholic trustee and Lakehead University communications professional.", "Trustee", nil, electionSource("https://www.lakeheadu.ca/users/B/lmbadana", "Lakehead University profile")),
				electionCandidate("Mike Bertoldo", "Bertoldo, Mike", "No verified public biography has been added from the sources reviewed.", "", nil),
				electionCandidate("Anthony Foglia", "Foglia, Anthony", "A local telecommunications professional with municipal committee experience.", "", nil),
				electionCandidate("Leanne Fonso", "Fonso, Leanne", "A current Thunder Bay Catholic trustee.", "Trustee", nil),
				electionCandidate("Olivia Kembel", "Kembel, Olivia", "A current student trustee and youth community advocate.", "Student Trustee", nil),
				electionCandidate("Matt Pearson", "Pearson, Matt", "No verified current public biography has been added from the sources reviewed.", "", nil),
				electionCandidate("Tony Romeo", "Romeo, Tony", "A current Thunder Bay Catholic trustee.", "Trustee", nil),
			},
		},
		{
			ID: "trustee-french-public", Name: "French Public School Board Trustee", Seats: 1, Choice: "Choose 1", Acclaimed: true,
			Intro: "All seats acclaimed.",
			Candidates: []ElectionCandidateView{
				electionCandidate("Anne-Marie Gélineault", "Gélineault, Anne-Marie", "The current local trustee for Conseil scolaire public du Grand Nord.", "Trustee", nil),
			},
		},
		{
			ID: "trustee-french-separate", Name: "French Separate School Board Trustee", Seats: 5, Choice: "Acclaimed", Acclaimed: true,
			Intro: "All seats acclaimed.",
			Candidates: []ElectionCandidateView{
				electionCandidate("Angele Desbiens", "Desbiens, Angele", "A current Conseil scolaire catholique des Aurores boréales trustee.", "Trustee", nil),
				electionCandidate("Claudette Gleeson", "Gleeson, Claudette", "A current Conseil scolaire catholique des Aurores boréales trustee.", "Trustee", nil),
				electionCandidate("Elodie Grunerud", "Grunerud, Elodie", "A current Conseil scolaire catholique des Aurores boréales trustee.", "Trustee", nil),
				electionCandidate("Victoria Mauro", "Mauro, Victoria", "A current Conseil scolaire catholique des Aurores boréales trustee.", "Trustee", nil),
				electionCandidate("Donald Pelletier", "Pelletier, Donald", "No verified public biography has been added from the sources reviewed.", "", nil),
			},
		},
	}

	return Election2026ViewModel{
		OfficialSource: models.SourceRef{
			URL:   Election2026OfficialURL,
			Label: "City of Thunder Bay election information",
		},
		ElectionDayDate:  "Monday, October 26",
		ElectionDayHours: "10 a.m.–8 p.m.",
		AdvanceVoteDates: "October 19 and 21",
		AdvanceVoteHours: "10 a.m.–8 p.m.",
		Mayor:            mayor,
		AtLarge:          atLarge,
		Wards:            wards,
		Trustees:         trustees,
	}
}

// MunicipalCandidateCount returns only Mayor, At-Large and ward candidates.
func (vm Election2026ViewModel) MunicipalCandidateCount() int {
	n := len(vm.Mayor.Candidates) + len(vm.AtLarge.Candidates)
	for _, ward := range vm.Wards {
		n += len(ward.Candidates)
	}
	return n
}

func (vm Election2026ViewModel) TrusteeCandidateCount() int {
	n := 0
	for _, contest := range vm.Trustees {
		n += len(contest.Candidates)
	}
	return n
}
