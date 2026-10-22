// fairshare-demo builds a demo database: a made-up guild that has raided for a few weeks,
// for showing the community website before launch. Nothing is written by hand: people
// register, sign up to events and get rostered; raids open with real snapshots; every
// auction is a real commit-reveal transcript whose winner and price the replay itself
// decides; each raid settles through the same upload path the bot uses. So every number
// on the site is consistent and the balances sum to zero, exactly as they will for real.
//
//	go run ./backend/cmd/fairshare-demo -db data/demo.db
//
// Then serve it with FAIRSHARE_DEMO=1 and FAIRSHARE_DB_PATH=data/demo.db (no Discord
// token: demo mode never runs the bot). Every name in it is invented.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/replay"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// DemoMarker is the guild_config key that marks a demo database. The backend refuses to
// run the bot on a database carrying it, and refuses demo mode on one without it.
const DemoMarker = "demoDatabase"

func main() {
	path := flag.String("db", "data/demo.db", "demo database file to create")
	force := flag.Bool("force", false, "replace the file if it exists")
	seed := flag.Int64("seed", 7, "random seed (the same seed gives the same guild)")
	weeks := flag.Int("weeks", 6, "weeks of raiding to simulate, ending today")
	flag.Parse()

	if _, err := os.Stat(*path); err == nil {
		if !*force {
			log.Fatalf("%s already exists; pass -force to replace it", *path)
		}
		// A silent failure here would append a second guild to the old file, so a locked
		// database (the backend is serving it) stops the run instead.
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if err := os.Remove(*path + suffix); err != nil && !os.IsNotExist(err) {
				log.Fatalf("cannot replace %s: %v (stop the backend serving it first, or use another -db path)", *path+suffix, err)
			}
		}
	}
	st, err := store.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	p := &people.Service{S: st}
	b := &bans.Service{S: st, People: p}
	p.IsBannedIdentity = b.IsBannedIdentity
	g := &gen{st: st, p: p, r: &raids.Service{S: st, People: p, Bans: b}, rng: rand.New(rand.NewSource(*seed))}
	if err := g.run(*weeks); err != nil {
		log.Fatal(err)
	}
	if err := st.SetConfig(DemoMarker, "true"); err != nil {
		log.Fatal(err)
	}
	log.Printf("demo database written to %s", *path)
}

// ---------------------------------------------------------------- the guild

type member struct {
	id, display, char, class, spec, role string
	alt, altClass                        string
	skill                                float64 // typical log percentile
	reliability                          float64 // chance of signing up
	greed                                float64 // how much of their budget they put on an item they want
	leader                               bool
	joinedWeek                           int // first week they raid
}

// An invented guild: names are made up and match no real player.
var guild = []member{
	{display: "Bright", char: "Brightmane", class: "Paladin", spec: "Protection", role: "tank", skill: 78, reliability: 1, greed: .5, leader: true},
	{display: "quill", char: "Quillfeather", class: "Priest", spec: "Holy", role: "healer", skill: 85, reliability: 1, greed: .4, leader: true},
	{display: "StoneyB", char: "Stonebrew", class: "Warrior", spec: "Protection", role: "tank", skill: 70, reliability: .95, greed: .6, alt: "Brewbelly", altClass: "Shaman"},
	{display: "ash", char: "Ashwhisper", class: "Warlock", spec: "Destruction", role: "ranged", skill: 92, reliability: .95, greed: .8},
	{display: "Wick", char: "Thornwick", class: "Druid", spec: "Restoration", role: "healer", skill: 66, reliability: .9, greed: .3, alt: "Thornbough", altClass: "Druid"},
	{display: "Dusky", char: "Duskvale", class: "Rogue", spec: "Combat", role: "melee", skill: 88, reliability: .9, greed: .9},
	{display: "ember", char: "Emberline", class: "Mage", spec: "Fire", role: "ranged", skill: 95, reliability: .9, greed: .7},
	{display: "Petal", char: "Frostpetal", class: "Mage", spec: "Frost", role: "ranged", skill: 61, reliability: .85, greed: .5, alt: "Frostwhisker", altClass: "Priest"},
	{display: "Gravel", char: "Gravelgrip", class: "Warrior", spec: "Fury", role: "melee", skill: 74, reliability: .9, greed: 1},
	{display: "reedy", char: "Hollowreed", class: "Hunter", spec: "Marksmanship", role: "ranged", skill: 57, reliability: .8, greed: .6},
	{display: "Mossy", char: "Ironmoss", class: "Shaman", spec: "Restoration", role: "healer", skill: 72, reliability: .9, greed: .35},
	{display: "Juni", char: "Juniperhex", class: "Warlock", spec: "Affliction", role: "ranged", skill: 69, reliability: .8, greed: .5, alt: "Juniperbark", altClass: "Warrior"},
	{display: "Kettle", char: "Kettleburn", class: "Priest", spec: "Shadow", role: "ranged", skill: 63, reliability: .75, greed: .45, alt: "Kettlewick", altClass: "Mage"},
	{display: "LJ", char: "Lanternjaw", class: "Paladin", spec: "Holy", role: "healer", skill: 80, reliability: .9, greed: .3},
	{display: "glen", char: "Mossglen", class: "Druid", spec: "Feral", role: "melee", skill: 54, reliability: .7, greed: .7},
	{display: "Sparky", char: "Nettlespark", class: "Hunter", spec: "Beast Mastery", role: "ranged", skill: 76, reliability: .85, greed: .55},
	{display: "Oak", char: "Oakenshade", class: "Warrior", spec: "Arms", role: "melee", skill: 81, reliability: .85, greed: .75},
	{display: "Pebbles", char: "Pebbleshank", class: "Rogue", spec: "Assassination", role: "melee", skill: 59, reliability: .8, greed: .65},
	{display: "Quartz", char: "Quartzwing", class: "Mage", spec: "Arcane", role: "ranged", skill: 84, reliability: .9, greed: .6},
	{display: "rusty", char: "Rustleaf", class: "Shaman", spec: "Enhancement", role: "melee", skill: 67, reliability: .75, greed: .8},
	{display: "Salty", char: "Saltmarsh", class: "Priest", spec: "Discipline", role: "healer", skill: 77, reliability: .85, greed: .3, alt: "Saltlick", altClass: "Rogue"},
	{display: "Tides", char: "Tidecaller", class: "Shaman", spec: "Elemental", role: "ranged", skill: 71, reliability: .8, greed: .55},
	{display: "Umber", char: "Umberfang", class: "Hunter", spec: "Survival", role: "ranged", skill: 48, reliability: .7, greed: .5, joinedWeek: 2},
	{display: "Cog", char: "Velvetcog", class: "Rogue", spec: "Combat", role: "melee", skill: 73, reliability: .8, greed: .7, joinedWeek: 3},
	{display: "Wisp", char: "Wispwhistle", class: "Druid", spec: "Balance", role: "ranged", skill: 65, reliability: .75, greed: .5, joinedWeek: 3},
	{display: "Yarrow", char: "Yarrowmend", class: "Paladin", spec: "Retribution", role: "melee", skill: 52, reliability: .7, greed: .6, joinedWeek: 4},
}

// ---------------------------------------------------------------- the instances

type item struct {
	id      int64
	name    string
	slot    string // floor table key
	quality string // link colour
}

type boss struct {
	id   int64
	name string
	loot []item
}

type instance struct {
	name   string
	title  string // event title
	bosses []boss
}

const epic, legendary = "a335ee", "ff8000"

var molten = instance{name: "Molten Core", title: "Molten Core", bosses: []boss{
	{663, "Lucifron", []item{{19145, "Robe of Volatile Power", "INVTYPE_CHEST", epic}, {18870, "Helm of the Lifegiver", "INVTYPE_HEAD", epic}}},
	{664, "Magmadar", []item{{17069, "Striker's Mark", "INVTYPE_RANGED", epic}, {18822, "Obsidian Edged Blade", "INVTYPE_2HWEAPON", epic}, {17065, "Medallion of Steadfast Might", "DEFAULT", epic}}},
	{665, "Gehennas", []item{{19146, "Wristguards of Stability", "DEFAULT", epic}, {18879, "Heavy Dark Iron Ring", "DEFAULT", epic}}},
	{666, "Garr", []item{{17105, "Aurastone Hammer", "INVTYPE_WEAPON", epic}, {18832, "Brutality Blade", "INVTYPE_WEAPON", epic}, {19019, "Thunderfury, Blessed Blade of the Windseeker", "INVTYPE_WEAPON", legendary}}},
	{667, "Shazzrah", []item{{19147, "Ring of Spell Power", "DEFAULT", epic}, {17077, "Crimson Shocker", "INVTYPE_RANGED", epic}}},
	{668, "Baron Geddon", []item{{18820, "Talisman of Ephemeral Power", "INVTYPE_TRINKET", epic}, {18821, "Quick Strike Ring", "DEFAULT", epic}}},
	{669, "Golemagg the Incinerator", []item{{17103, "Azuresong Mageblade", "INVTYPE_WEAPON", epic}, {18842, "Staff of Dominance", "INVTYPE_2HWEAPON", epic}}},
	{670, "Sulfuron Harbinger", []item{{17074, "Shadowstrike", "INVTYPE_2HWEAPON", epic}, {19137, "Onslaught Girdle", "DEFAULT", epic}}},
	{671, "Majordomo Executus", []item{{18811, "Fireproof Cloak", "DEFAULT", epic}, {18809, "Sash of Whispered Secrets", "DEFAULT", epic}}},
	{672, "Ragnaros", []item{{17076, "Bonereaver's Edge", "INVTYPE_2HWEAPON", epic}, {18816, "Perdition's Blade", "INVTYPE_WEAPON", epic}, {17063, "Band of Accuria", "DEFAULT", epic}, {17204, "Eye of Sulfuras", "DEFAULT", legendary}}},
}}

var blackwing = instance{name: "Blackwing Lair", title: "Blackwing Lair", bosses: []boss{
	{610, "Razorgore the Untamed", []item{{19336, "Arcane Infused Gem", "INVTYPE_TRINKET", epic}, {19335, "Spineshatter", "INVTYPE_WEAPON", epic}}},
	{611, "Vaelastrasz the Corrupt", []item{{19339, "Mind Quickening Gem", "INVTYPE_TRINKET", epic}, {19348, "Red Dragonscale Protector", "DEFAULT", epic}}},
	{612, "Broodlord Lashlayer", []item{{19341, "Lifegiving Gem", "INVTYPE_TRINKET", epic}, {19351, "Maladath, Runed Blade of the Black Flight", "INVTYPE_WEAPON", epic}}},
	{613, "Firemaw", []item{{19353, "Drake Talon Cleaver", "INVTYPE_2HWEAPON", epic}, {19365, "Claw of the Black Drake", "INVTYPE_WEAPON", epic}}},
	{614, "Ebonroc", []item{{19368, "Dragonbreath Hand Cannon", "INVTYPE_RANGED", epic}, {19406, "Drake Fang Talisman", "INVTYPE_TRINKET", epic}}},
	{615, "Flamegor", []item{{19367, "Dragon's Touch", "INVTYPE_RANGED", epic}, {19357, "Herald of Woe", "INVTYPE_2HWEAPON", epic}}},
	{616, "Chromaggus", []item{{19347, "Claw of Chromaggus", "INVTYPE_WEAPON", epic}, {19352, "Chromatically Tempered Sword", "INVTYPE_WEAPON", epic}, {19361, "Ashjre'thul, Crossbow of Smiting", "INVTYPE_RANGED", epic}}},
	{617, "Nefarian", []item{{19364, "Ashkandi, Greatsword of the Brotherhood", "INVTYPE_2HWEAPON", epic}, {19360, "Lok'amir il Romathis", "INVTYPE_WEAPON", epic}, {19356, "Staff of the Shadow Flame", "INVTYPE_2HWEAPON", epic}, {19379, "Neltharion's Tear", "INVTYPE_TRINKET", epic}}},
}}

var onyxia = instance{name: "Onyxia's Lair", title: "Sunday Onyxia", bosses: []boss{
	{1084, "Onyxia", []item{{17068, "Deathbringer", "INVTYPE_WEAPON", epic}, {18813, "Ring of Binding", "DEFAULT", epic}, {17078, "Sapphiron Drape", "DEFAULT", epic}, {18205, "Eskhandar's Collar", "DEFAULT", epic}}},
}}

// floors are the community rule (docs/rules.md): every item 10, two-handed weapons 20.
var floors = map[string]int64{"DEFAULT": 10, "INVTYPE_2HWEAPON": 20}

// floorFor picks the floor-table key the addon would use for an equip location (the
// location when the table has a row for it, else DEFAULT) and its floor.
func floorFor(slot string) (int64, string) {
	if v, ok := floors[slot]; ok {
		return v, slot
	}
	return floors["DEFAULT"], "DEFAULT"
}

func link(it item) string {
	return fmt.Sprintf("|cff%s|Hitem:%d::::::::60:::::::::|h[%s]|h|r", it.quality, it.id, it.name)
}

const hearthstone = "|cffffffff|Hitem:6948::::::::1:::::::::|h[Hearthstone]|h|r"

// ---------------------------------------------------------------- generator

type gen struct {
	st  *store.Store
	p   *people.Service
	r   *raids.Service
	rng *rand.Rand

	voided, transfers, noreveals int // keep a few of each across the season
	moltenNights                 int // Molten Core nights so far, for the scripted legendaries
}

func (g *gen) clock(t time.Time) { store.Now = func() int64 { return t.Unix() } }

func (g *gen) run(weeks int) error {
	defer func() { store.Now = func() int64 { return time.Now().Unix() } }()
	// Raid nights: Tuesday and Thursday at 19:30 US Eastern, Onyxia some Sundays, ending today.
	east, _ := time.LoadLocation("America/New_York")
	today := time.Now().In(east)
	start := time.Date(today.Year(), today.Month(), today.Day(), 19, 30, 0, 0, east).AddDate(0, 0, -7*weeks)
	g.clock(start.AddDate(0, 0, -14))
	for k, v := range floors {
		if _, err := g.st.DB.Exec(`INSERT INTO floor_price (slot, value) VALUES (?, ?) ON CONFLICT(slot) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	for i := range guild {
		m := &guild[i]
		m.id = fmt.Sprintf("9%017d", i+1) // invented ids in Discord's shape; never shown on the site
		created := start.AddDate(-2, 0, -i*11)
		if _, err := g.p.Register(m.id, m.display, created, m.char, m.class, m.role); err != nil {
			return fmt.Errorf("register %s: %w", m.char, err)
		}
		if m.alt != "" {
			if _, err := g.p.Register(m.id, m.display, created, m.alt, m.altClass); err != nil {
				return fmt.Errorf("register %s: %w", m.alt, err)
			}
		}
	}

	var nights []struct {
		at   time.Time
		inst instance
		week int
	}
	for day := start; day.Before(today); day = day.AddDate(0, 0, 1) {
		week := int(day.Sub(start).Hours() / (24 * 7))
		switch day.Weekday() {
		case time.Tuesday:
			nights = append(nights, struct {
				at   time.Time
				inst instance
				week int
			}{day, molten, week})
		case time.Thursday:
			if week >= 2 { // the guild clears Molten Core first, then adds Blackwing Lair
				nights = append(nights, struct {
					at   time.Time
					inst instance
					week int
				}{day, blackwing, week})
			}
		case time.Sunday:
			if week%2 == 1 {
				nights = append(nights, struct {
					at   time.Time
					inst instance
					week int
				}{day, onyxia, week})
			}
		}
	}
	for i, n := range nights {
		if sameDay(n.at, today) {
			continue // tonight's raid is the live one below
		}
		if err := g.raidNight(n.at, n.inst, n.week, i, false); err != nil {
			return fmt.Errorf("raid on %s (%s): %w", n.at.Format("Mon 2 Jan"), n.inst.name, err)
		}
	}
	// Tonight: a raid under way, shown from the leader's newest live update.
	tonight := time.Now().Add(-100 * time.Minute)
	if err := g.raidNight(tonight, molten, weeks, len(nights), true); err != nil {
		return fmt.Errorf("tonight's raid: %w", err)
	}
	return nil
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// raidNight runs one scheduled raid: event, signups, roster, open, play, settle, scores.
// With live set, it stops halfway and sends a live update instead of settling.
func (g *gen) raidNight(at time.Time, inst instance, week, n int, live bool) error {
	leader := &guild[n%2] // the two officers take turns
	if inst.name == molten.name && !live {
		g.moltenNights++
	}
	g.clock(at.AddDate(0, 0, -3))
	ev, err := g.r.CreateEvent(inst.title, inst.name, at.Unix(), leader.id, 20, 5, 0, 0)
	if err != nil {
		return err
	}

	// Who signs up: everyone who has joined, by their reliability; the leader always does.
	var signed []*member
	for i := range guild {
		m := &guild[i]
		if week < m.joinedWeek {
			continue
		}
		if m == leader || g.rng.Float64() < m.reliability {
			signed = append(signed, m)
		}
	}
	g.rng.Shuffle(len(signed), func(i, j int) { signed[i], signed[j] = signed[j], signed[i] })
	sort.SliceStable(signed, func(i, j int) bool { return signed[i] == leader && signed[j] != leader })
	// Players with an alt sometimes bring it, and now and then swap to it mid-raid, so the
	// site has both cases to show.
	names, swapAt := map[*member]string{}, map[*member]int{}
	for _, m := range signed {
		names[m] = m.char
		if m.alt == "" || m == leader {
			continue
		}
		switch {
		case g.rng.Float64() < 0.3:
			names[m] = m.alt
		case g.rng.Float64() < 0.25 && len(inst.bosses) > 3:
			swapAt[m] = 2 + g.rng.Intn(len(inst.bosses)-3)
		}
	}
	g.clock(at.AddDate(0, 0, -2))
	confirmed := map[string]bool{}
	var roster, standby []*member
	for i, m := range signed {
		isStandby := i >= 20
		role := m.role
		if names[m] != m.char && !slices.Contains(people.RolesFor(m.altClass), role) {
			role = people.RolesFor(m.altClass)[0] // the alt plays what its class can
		}
		if _, err := g.r.Signup(ev.ID, m.id, m.display, names[m], role, isStandby); err != nil {
			return fmt.Errorf("signup %s: %w", names[m], err)
		}
		if isStandby {
			standby = append(standby, m)
		} else {
			roster = append(roster, m)
			confirmed[m.id] = true
		}
	}
	g.clock(at.AddDate(0, 0, -1))
	ids := make([]string, 0, len(confirmed))
	for id := range confirmed {
		ids = append(ids, id)
	}
	if err := g.r.ConfirmRoster(ev.ID, ids, leader.id); err != nil {
		return err
	}

	g.clock(at.Add(-15 * time.Minute))
	raid, err := g.r.Open(ev.ID, "", leader.id)
	if err != nil {
		return err
	}
	snap, err := snapshot.Parse(raid.SnapshotText)
	if err != nil {
		return err
	}
	_ = standby

	// Someone occasionally does not show, someone arrives late or leaves early.
	var present []*member
	for _, m := range roster {
		if m != leader && g.rng.Float64() < 0.04 {
			continue
		}
		present = append(present, m)
	}
	late, early := map[*member]int{}, map[*member]int{}
	if len(inst.bosses) > 3 {
		for _, m := range present {
			switch {
			case m != leader && g.rng.Float64() < 0.06:
				late[m] = 1 + g.rng.Intn(2)
			case m != leader && g.rng.Float64() < 0.05:
				early[m] = len(inst.bosses) - 1 - g.rng.Intn(2)
			}
		}
	}

	t := &script{g: g, raidID: raid.ID, snap: snap, checksum: raid.SnapshotChecksum, leader: leader.char, clock: at.Add(5 * time.Minute).Unix(), spent: map[string]int64{}, names: names, swapAt: swapAt}
	t.add(leader.char, "SNAP", fmt.Sprint(raid.ID), raid.SnapshotChecksum, fmt.Sprint(snap.Issued), fmt.Sprint(snap.OffspecPct), snap.FloorsCSV(), strings.Join(sortedCopy(snap.Banned), ","))
	var rows []string
	for _, m := range present {
		for _, r := range snap.Rows {
			if r.Character == m.char || (m.alt != "" && r.Character == m.alt) {
				rows = append(rows, fmt.Sprintf("%s,%s,%d,%d", r.Character, r.Owner, r.Balance, r.Credit))
			}
		}
	}
	sort.Strings(rows)
	t.add(leader.char, "ROWS", rows...)

	// The test auction: everyone's addon bids the minimum.
	if err := t.auction(item{name: "Hearthstone"}, hearthstone, "test", 1, "DEFAULT", present, true); err != nil {
		return err
	}

	stopAt := len(inst.bosses)
	if live {
		stopAt = (len(inst.bosses) + 1) / 2
	}
	for bi, bs := range inst.bosses[:stopAt] {
		t.boss = bi
		t.clock += int64(20+g.rng.Intn(25)) * 60
		var killers []*member
		for _, m := range present {
			if bi < late[m] || (early[m] > 0 && bi >= early[m]) {
				continue
			}
			killers = append(killers, m)
		}
		names := make([]string, 0, len(killers))
		for _, m := range killers {
			names = append(names, t.name(m))
		}
		sort.Strings(names)
		t.add(leader.char, "KILL", fmt.Sprint(bs.id), bs.name, strings.Join(names, ","))

		drops := 1 + g.rng.Intn(2)
		if len(inst.bosses) == 1 {
			drops = 2
		}
		var pool []item
		var forced []item
		for _, it := range bs.loot {
			// Legendaries are rare. The season scripts one of each so the demo shows them:
			// Thunderfury on the third Molten Core night, the Eye of Sulfuras on the sixth.
			if it.quality == legendary {
				if (it.id == 19019 && g.moltenNights == 3) || (it.id == 17204 && g.moltenNights == 6) {
					forced = append(forced, it)
				}
				continue
			}
			pool = append(pool, it)
		}
		g.rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		pool = append(forced, pool...)
		if drops > len(pool) {
			drops = len(pool)
		}
		for _, it := range pool[:drops] {
			t.clock += 60
			floor, key := floorFor(it.slot)
			if err := t.auction(it, link(it), "loot", floor, key, killers, false); err != nil {
				return err
			}
		}
		if live && bi == stopAt-1 && len(pool) > 0 {
			// One auction still bidding when the leader reloaded.
			t.clock += 60
			floor, key := floorFor(pool[0].slot)
			t.open(link(pool[0]), "loot", floor, key)
		}
	}

	g.clock(time.Unix(t.clock+600, 0))
	if live {
		out, err := g.r.Progress(raid.ID, leader.id, t.text())
		if err != nil {
			return err
		}
		log.Printf("raid %d %s %s: live update (%s)", raid.ID, at.Format("Mon 2 Jan"), inst.name, out.Status)
		return nil
	}
	out, err := g.r.Upload(raid.ID, leader.id, t.text())
	if err != nil {
		return err
	}
	if out.Status != "settled" {
		var flags []string
		if out.Report != nil {
			flags = out.Report.Disputing()
		}
		return fmt.Errorf("did not settle: %s (%s) disputing: %v", out.Status, out.Message, flags)
	}
	if err := g.score(raid.ID, present, t.name); err != nil {
		return err
	}
	log.Printf("raid %d %s %s: settled, %d raiders, %d auctions", raid.ID, at.Format("Mon 2 Jan"), inst.name, len(present), t.auctions)
	return nil
}

// score writes performance scores for a settled raid and pays its pool out by band, the
// way the brief proposes: 95+ three units, 75-94 two, 50-74 one, 25-49 half,
// plus a unit for tanks and healers. What does not divide evenly stays in the pool.
func (g *gen) score(raidID int64, present []*member, nameOf func(*member) string) error {
	var pool int64
	if err := g.st.DB.QueryRow(`SELECT COALESCE(SUM(delta), 0) FROM ledger WHERE person_id = ?`, fmt.Sprintf("pool:%d", raidID)).Scan(&pool); err != nil {
		return err
	}
	type scored struct {
		m     *member
		pct   float64
		band  string
		units float64
	}
	var all []scored
	var total float64
	for _, m := range present {
		pct := math.Round(math.Max(3, math.Min(99, m.skill+g.rng.NormFloat64()*9)))
		s := scored{m: m, pct: pct}
		switch {
		case pct >= 95:
			s.band, s.units = "95+", 3
		case pct >= 75:
			s.band, s.units = "75-94", 2
		case pct >= 50:
			s.band, s.units = "50-74", 1
		case pct >= 25:
			s.band, s.units = "25-49", 0.5
		default:
			s.band = "below 25"
		}
		if s.units > 0 && (m.role == "tank" || m.role == "healer") {
			s.units++
		}
		total += s.units
		all = append(all, s)
	}
	rid := raidID
	var entries []store.Entry
	for _, s := range all {
		var award int64
		if total > 0 && pool > 0 {
			award = int64(math.Floor(float64(pool) * s.units / total))
		}
		if _, err := g.st.DB.Exec(`INSERT INTO performance (raid_id, person_id, spec, percentile, band, award, character) VALUES (?, ?, ?, ?, ?, ?, ?)`, raidID, s.m.id, s.m.spec, s.pct, s.band, award, nameOf(s.m)); err != nil {
			return err
		}
		if award > 0 {
			entries = append(entries,
				store.Entry{RaidID: &rid, PersonID: fmt.Sprintf("pool:%d", raidID), Delta: -award, Reason: "pool", Note: "performance payout"},
				store.Entry{RaidID: &rid, PersonID: s.m.id, Delta: award, Reason: "award", Character: nameOf(s.m)})
		}
	}
	return store.AppendLedger(g.st.DB, entries)
}

// ---------------------------------------------------------------- transcript

// script writes one raid's transcript as the leader's addon would record it.
type script struct {
	g        *gen
	raidID   int64
	snap     snapshot.Snapshot
	checksum string
	leader   string
	clock    int64
	lines    []string
	counter  int
	auctions int
	spent    map[string]int64   // owner -> owed for this raid's sales so far
	names    map[*member]string // the character each member plays tonight
	swapAt   map[*member]int    // boss index from which a member is on their alt instead
	boss     int                // current boss index
}

// name is the character a member is on right now: tonight's choice, or the alt once
// they have swapped mid-raid.
func (t *script) name(m *member) string {
	if at, ok := t.swapAt[m]; ok && t.boss >= at && m.alt != "" {
		return m.alt
	}
	if n, ok := t.names[m]; ok && n != "" {
		return n
	}
	return m.char
}

func esc(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "^", "~1") }

func (t *script) add(sender, msgType string, fields ...string) {
	parts := make([]string, 0, len(fields)+1)
	parts = append(parts, msgType)
	for _, f := range fields {
		parts = append(parts, esc(f))
	}
	t.lines = append(t.lines, fmt.Sprintf("%d\tin\t%s\t%s", t.clock, sender, strings.Join(parts, "^")))
}

func (t *script) text() string {
	return fmt.Sprintf("FAIRSHARE TRANSCRIPT 1\nraid=%d\nsnapshot=%s\nplayer=%s\nversion=demo\n%s", t.raidID, t.checksum, t.leader, strings.Join(t.lines, "\n"))
}

// replay runs the real rules over the transcript so far.
func (t *script) replay() (*replay.Report, error) {
	tr, err := replay.ParseTranscript(t.text())
	if err != nil {
		return nil, err
	}
	return replay.Run(t.snap, t.checksum, tr, replay.Options{})
}

func (t *script) row(char string) (snapshot.Row, bool) {
	for _, r := range t.snap.Rows {
		if r.Character == char {
			return r, true
		}
	}
	return snapshot.Row{}, false
}

// budget is what a bidder can still bid this raid: balance + credit - already owed.
func (t *script) budget(char string) int64 {
	r, ok := t.row(char)
	if !ok {
		return 0
	}
	return r.Balance + r.Credit - t.spent[r.Owner]
}

func (t *script) open(itemLink, kind string, floor int64, slot string) string {
	t.counter++
	id := fmt.Sprintf("%d-%d", t.raidID, t.counter)
	t.add(t.leader, "OPEN", id, kind, itemLink, fmt.Sprint(floor), fmt.Sprint(t.clock+20), slot)
	return id
}

func salt(rng *rand.Rand) string {
	b := make([]byte, 16)
	rng.Read(b)
	return hex.EncodeToString(b)
}

type bid struct {
	who     *member
	amount  int64
	offspec bool
	salt    string
}

// auction runs one auction from OPEN to RESULT. The replay decides the winner and price.
func (t *script) auction(it item, itemLink, kind string, floor int64, slot string, eligible []*member, everyone bool) error {
	g := t.g
	open := t.clock
	id := t.open(itemLink, kind, floor, slot)
	t.auctions++

	var bids []bid
	if everyone {
		for _, m := range eligible {
			bids = append(bids, bid{who: m, amount: 1, salt: salt(g.rng)})
		}
	} else {
		// A few raiders want each item; legendaries draw a crowd.
		want := 0.18
		if it.quality == legendary {
			want = 0.6
		}
		for _, m := range eligible {
			if g.rng.Float64() >= want {
				continue
			}
			budget := t.budget(t.name(m))
			if budget < floor {
				continue
			}
			top := floor + int64(float64(budget-floor)*m.greed*(0.3+0.7*g.rng.Float64()))
			if top > budget {
				top = budget
			}
			amount := floor + g.rng.Int63n(top-floor+1)
			bids = append(bids, bid{who: m, amount: amount, offspec: g.rng.Float64() < 0.08, salt: salt(g.rng)})
		}
	}

	// A sealed bid is lost now and then: committed, never revealed.
	lost := -1
	if !everyone && len(bids) >= 3 && g.noreveals < 3 && g.rng.Float64() < 0.08 {
		lost = g.rng.Intn(len(bids))
		g.noreveals++
	}

	var committers []string
	for i, b := range bids {
		t.clock = open + 2 + int64(i*15/max(1, len(bids)))
		off := 0
		if b.offspec {
			off = 1
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%s", id, t.name(b.who), b.amount, off, b.salt)))
		t.add(t.name(b.who), "COMMIT", id, hex.EncodeToString(hash[:]))
		committers = append(committers, t.name(b.who))
	}
	sort.Strings(committers)
	t.clock = open + 21
	t.add(t.leader, "CLOSE", id, strings.Join(committers, ","))
	for i, b := range bids {
		if i == lost {
			continue
		}
		t.clock = open + 22 + int64(i%4)
		off := "0"
		if b.offspec {
			off = "1"
		}
		t.add(t.name(b.who), "REVEAL", id, fmt.Sprint(b.amount), off, b.salt)
	}
	t.clock = open + 27
	if lost >= 0 {
		t.clock = open + 36 // the leader waits out the reveal window for the missing bid
	}

	rep, err := t.replay()
	if err != nil {
		return err
	}
	var a *replay.Auction
	for _, x := range rep.Auctions {
		if x.ID == id {
			a = x
		}
	}
	if a == nil {
		return errors.New("auction vanished from the replay")
	}
	t.add(t.leader, "RESULT", id, a.Winner, fmt.Sprint(a.Price))
	if kind != "loot" || a.Winner == "" {
		return nil
	}
	winner, _ := t.row(a.Winner)
	t.spent[winner.Owner] += a.Price

	// Now and then the winner hands the item to someone it suits better, at the same price.
	if g.transfers < 4 && g.rng.Float64() < 0.06 {
		for _, m := range eligible {
			r, _ := t.row(t.name(m))
			if t.name(m) == a.Winner || r.Owner == winner.Owner || t.budget(t.name(m)) < a.Price+5 {
				continue
			}
			t.clock += 30
			t.add(t.leader, "TRANSFER", id, a.Winner, t.name(m))
			if rep, err := t.replay(); err == nil && transferOK(rep, id) {
				t.spent[winner.Owner] -= a.Price
				t.spent[r.Owner] += a.Price
				g.transfers++
			} else {
				t.lines = t.lines[:len(t.lines)-1]
			}
			break
		}
	}

	// Once in the season a sale is voided (a mis-click) and the item auctioned again.
	if g.voided == 0 && g.rng.Float64() < 0.05 {
		t.clock += 40
		t.add(t.leader, "VOID", id, "re-run")
		t.spent[winner.Owner] -= a.Price
		g.voided++
		t.clock += 20
		return t.auction(it, itemLink, kind, floor, slot, eligible, false)
	}
	return nil
}

func transferOK(rep *replay.Report, id string) bool {
	for _, a := range rep.Auctions {
		if a.ID != id {
			continue
		}
		for _, f := range a.Flags {
			if strings.HasPrefix(f, "bad_transfer") {
				return false
			}
		}
		return a.TransferredTo != ""
	}
	return false
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
