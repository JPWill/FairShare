// Package content holds the words the community reads in more than one place: the FAQ
// (website and #faq), the rules (website, #rules and docs/rules.md), the terms members
// accept in #welcome, and the getting-started steps. Every number is a parameter, so the
// text can never disagree with the settings the bot enforces.
package content

import (
	"fmt"
	"strings"
)

// DefaultSite is the community website. guild_config.websiteURL overrides it.
const DefaultSite = "https://fairsharewow.com"

// Repo is the public source code.
const Repo = "https://github.com/JPWill/FairShare"

// Params are the live settings the texts quote.
type Params struct {
	FloorDefault int64
	Floor2H      int64
	Newcomer     int64 // newcomer credit
	PerRaid      int64 // credit per raid attended
	Cap          int64 // credit cap
	PoolPct      int64
	FreePasses   int64
	Penalty      int64
	MinAge       int64 // Discord account age in days
	OffspecPct   int64
	Site         string // website, no trailing slash
	Discord      string // invite link, may be empty
}

// Example is the worked split the FAQ uses: a 200-share sale in a 20-player raid.
type Example struct{ Price, Raiders, Pool, ToRaid, Each int64 }

// Worked computes the example from the pool percentage.
func (p Params) Worked() Example {
	const price, raiders int64 = 200, 20
	pool := price * p.PoolPct / 100
	return Example{Price: price, Raiders: raiders, Pool: pool, ToRaid: price - pool, Each: (price - pool) / raiders}
}

func (p Params) site() string { return strings.TrimRight(p.Site, "/") }

// QA is one question and its answer. ID is a stable anchor for links.
type QA struct {
	ID string
	Q  string
	A  string
}

// FAQ is the community FAQ in the order a sceptical raider asks the questions. Answers
// are short and plain; URLs stand on their own so Discord links them and the website
// wraps them in anchors.
func (p Params) FAQ() []QA {
	ex := p.Worked()
	site := p.site()
	poolClause := ""
	if p.PoolPct > 0 {
		poolClause = fmt.Sprintf(" and %d goes to the performance pool", ex.Pool)
	}
	offspec := "The leader can set a discount for the night."
	if p.OffspecPct > 0 {
		offspec = fmt.Sprintf("Tonight's discount is %d%% off the floor.", p.OffspecPct)
	}
	passes := "one free pass"
	if p.FreePasses != 1 {
		passes = fmt.Sprintf("%d free passes", p.FreePasses)
	}
	pool := fmt.Sprintf("%d%% of every sale is held back for performance bonuses, paid by log percentile for your spec. Kills only; an avoidable death voids it. Until a logging site supports Forever the pool builds up and is paid out when scoring starts.", p.PoolPct)
	if p.PoolPct == 0 {
		pool = "A cut of every sale can go to performance bonuses, paid by log percentile. Until a logging site supports Forever that cut is set to 0%, so all of every sale goes to the raid."
	}
	minAge := ""
	if p.MinAge > 0 {
		minAge = fmt.Sprintf(" Your Discord account must be at least %d days old.", p.MinAge)
	}
	join := "Join the Discord"
	if p.Discord != "" {
		join = "Join the Discord at " + p.Discord
	}
	return []QA{
		{"gdkp", "Is this GDKP?",
			"No, and gold-for-loot runs are against Forever's rules anyway. No gold ever changes hands here. Shares are points you earn by raiding and spend on loot. They cannot be bought, sold or traded. Trading a won item for anything is a ban."},
		{"shares", "Where do shares come from?",
			fmt.Sprintf("From what winners pay. A %d-share sale in a %d-player raid means %d is split evenly among the raid (%d each)%s. Nothing is minted: every balance in the community adds up to zero. Check it on the Players page: %s/players", ex.Price, ex.Raiders, ex.ToRaid, ex.Each, poolClause, site)},
		{"debt", "Do I go into debt on my first win?",
			fmt.Sprintf("Yes, and that is fair: you took an item worth %d and had put in %d. Your credit limit on night one is %d shares, growing by %d per raid up to %d. Every later sale pays you, so it clears itself.", ex.Price, ex.Each, p.Newcomer, p.PerRaid, p.Cap)},
		{"sealed", "Can the leader see my bid?",
			"No. Your addon sends a locked commitment. Bids are opened only after bidding closes, and every addon in the raid works out the winner on its own. If the leader announces anything else, everyone's addon flags it and the raid cannot settle without officer review."},
		{"record", "Can the leader edit the record afterwards?",
			"No. Every raider's addon keeps the full transcript. Settlement replays raw messages on the server. If your copy disagrees with the leader's, upload it from the addon and the raid freezes for the officers. The code is public: " + Repo},
		{"second-price", "Why do I pay the second-highest bid?",
			"So you can bid what the item is worth to you and stop guessing. With first price everyone shades down and the best guesser wins. If nobody else bids, you pay the floor."},
		{"floor", "What is the floor?",
			fmt.Sprintf("The price of an item nobody else wanted: %d shares for any item, %d for a two-handed weapon. Once two people bid, the second price takes over. Floors are the same in every raid.", p.FloorDefault, p.Floor2H)},
		{"offspec", "Can I bid off-spec?",
			"Yes. Tick Off-spec on the bid window. " + offspec + " The flag is locked into your commitment, so it cannot change afterwards."},
		{"disconnect", "What if I disconnect mid-auction?",
			fmt.Sprintf("Your addon opens your bid automatically when bidding closes. If it never arrives you get %s per raid; after that it costs %d shares, because a bid that stays sealed is the one way to game a sealed auction.", passes, p.Penalty)},
		{"pool", "What is the performance pool?", pool},
		{"alts", "Do alts get their own shares?",
			"No. One share per person per kill. Shares, credit and bans belong to your Discord account; every character you register points at it. A second Discord account is a ban on every character linked to either." + minAge},
		{"addon", "Do I need the addon?",
			"To bid, yes. Without it you still earn your cut of every sale but cannot buy. It is one folder, no setup: a minimap button opens one window. The leader runs a test auction before the first boss."},
		{"safe", "What does the addon do to my game?",
			"Nothing outside loot. It sends raid addon messages, opens a bid window, and saves a transcript. It does not touch combat, movement or other addons, and it has no internet access."},
		{"disputes", "Who decides disputes?",
			"The officers, from the transcripts. Their call is final and logged. Most problems are caught by every addon in the raid at the time, so disputes are rare."},
		{"dies", "What if the community dies?",
			"Shares stop meaning anything, like DKP in a dead guild. There was never gold behind them. You keep the gear you won."},
		{"trust", "Why should I trust the people running this?",
			fmt.Sprintf("You do not have to. The code is public, every raid's record is on %s, and your own addon keeps a copy you can upload. Find a way to cheat it and we will fix it and say so.", site)},
		{"join", "How do I join?",
			join + ", accept the rules, press Register, install the addon, and sign up for a raid. Two minutes."},
		{"help", "Can I help run it?",
			"Yes. Raid leaders, officers and people who show up are what make it work. Say so in Discord. Lua or Go? The repository has issues marked for newcomers."},
	}
}

// Rules are the community rules, numbered. docs/rules.md carries the same text with the
// default numbers (a test keeps them in step).
func (p Params) Rules() []string {
	return []string{
		"**One person, one account.** Register every character and your Battle.net tag to your Discord. Points live on you, not a character. A second Discord account is a ban.",
		fmt.Sprintf("**Loot is auctioned for shares, never gold.** Want an item? Bid what it is worth to you. Bids are sealed; the winner pays the second-highest bid, or the floor price if nobody else bid (%d shares for any item, %d for a two-handed weapon). Gold never changes hands.", p.FloorDefault, p.Floor2H),
		fmt.Sprintf("**Everyone gets paid.** %d%% of every sale is split evenly among everyone in the raid, standby included. %d%% goes to the night's performance pool.", 100-p.PoolPct, p.PoolPct),
		"**Bids are final.** Credit lets you go negative up to your limit, which grows with every raid you attend. Earn it back by raiding.",
		"**Off-spec is fine.** Tick the box when you bid. The leader may set a discount for the night.",
		"**Won it, wear it.** The item goes to the winner. The raid leader can transfer it to another raider during the raid at the same price. Trading loot any other way is a ban.",
		"**Stay connected during auctions.** Your addon unseals your bid automatically when bidding closes. If that never arrives (a disconnect), you get one free pass per raid; after that it costs a penalty, because a bid that stays hidden is the one way to game the result.",
		"**Be there.** Every sale pays out to everyone present, standby included. Each boss kill you attend, and each full run, also counts toward your boss kills, which decide invite priority. Going AFK earns nothing.",
		"**Run the addon.** No addon, no bids. The leader runs a test auction before the first boss; fix anything it flags.",
		"**Everything is checked.** Every raider's addon verifies every auction and keeps the full record. The leader uploads it and shares move. Think something is off? Upload your own copy from the addon, any time: if it disagrees, the raid is frozen for the officers, whose call is final.",
	}
}

// RulesIntro and RulesOutro frame the numbered rules.
const RulesIntro = "**What we do.** Every boss kill belongs to everyone who was there. Loot is auctioned for **shares**, our own points, and the price is paid back out to the raid. Your shares follow you across every character. No DKP tables, no loot council, no favours."
const RulesOutro = "Cheating, bid rings, loot trading, and alt accounts get you banned on every character you have ever linked."

// Terms is what a member accepts in #welcome before the rest of the server opens.
func (p Params) Terms() string {
	return strings.Join([]string{
		"By pressing **Accept the rules** you agree to the community rules in #rules and to the following.",
		"",
		"**What we store.** Your Discord id and display name, the characters you register, and your Battle.net tag if you link it. We use them to keep one ledger per person and to keep banned people out. We never sell or share them.",
		"",
		fmt.Sprintf("**What is public.** Character names, raid attendance, items won, prices paid and share balances appear on the community website at %s and in this server. Discord ids, Battle.net tags and bans are never shown.", p.site()),
		"",
		"**No gold, no real money.** Shares are points inside this community. Buying, selling or trading them, or trading won items outside the system, is a ban.",
		"",
		"**One person, one account.** Registering a second Discord account, or an identity linked to a banned person, is a ban that follows every character and Battle.net tag involved.",
		"",
		"**Disputes.** Officers decide from the raid transcripts. Their decision is final and logged.",
		"",
		"**Leaving.** Ask an officer to remove your data at any time; ledger lines stay as anonymised history so the books still balance.",
		"",
		"You must be old enough to use Discord where you live. The officers may update these terms; changes are announced in #announcements.",
	}, "\n")
}

// GetStarted is the three-step onboarding card.
func (p Params) GetStarted() string {
	return strings.Join([]string{
		"**1. Register a character.** Press the button below (or open `/fairshare`). Name, class and roles: three clicks, no realm.",
		"**2. Install the addon.** Search for *FairShare* in the CurseForge app or WowUp, or download it from " + Repo + "/releases. One folder, nothing to configure.",
		"**3. Sign up for a raid.** Every raid has its own channel under Signups. Press Sign up there with a registered character.",
		"",
		fmt.Sprintf("Questions? Read #faq or %s/faq. Your balance and history: `/fairshare` here, or %s/players.", p.site(), p.site()),
	}, "\n")
}

// Section groups FAQ entries under a heading, in reading order, so the website can show
// cards instead of one long column. Discord posts the flat list.
type Section struct {
	Title string
	Items []QA
}

// topics is the reading order of the website's How It Works page: the money first, then
// whether it can be gamed, then raid night, then joining.
var topics = []struct {
	title string
	ids   []string
}{
	{"The basics", []string{"gdkp", "shares", "debt", "second-price", "floor"}},
	{"Can it be gamed?", []string{"sealed", "record", "disputes", "trust", "alts"}},
	{"On raid night", []string{"offspec", "disconnect", "pool", "addon", "safe"}},
	{"Joining", []string{"join", "help", "dies"}},
}

// Sections arranges the FAQ by topic. An entry whose id is in no topic lands in the last
// section, so a new question can never silently disappear from the page.
func (p Params) Sections() []Section {
	byID := map[string]QA{}
	for _, qa := range p.FAQ() {
		byID[qa.ID] = qa
	}
	out := make([]Section, 0, len(topics))
	placed := map[string]bool{}
	for _, t := range topics {
		s := Section{Title: t.title}
		for _, id := range t.ids {
			if qa, ok := byID[id]; ok {
				s.Items = append(s.Items, qa)
				placed[id] = true
			}
		}
		out = append(out, s)
	}
	for _, qa := range p.FAQ() {
		if !placed[qa.ID] {
			out[len(out)-1].Items = append(out[len(out)-1].Items, qa)
		}
	}
	return out
}
