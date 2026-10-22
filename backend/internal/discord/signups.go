package discord

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
)

// Signups: Sign up -> pick one of your characters -> pick a role its class can play.
// Officers confirm a roster; after that only Withdraw remains for members.

func signupButtons(ev raids.Event) []discordgo.MessageComponent {
	id := fmt.Sprint(ev.ID)
	if ev.RosterConfirmed() {
		return []discordgo.MessageComponent{row(
			button("Withdraw", "fs:signup:"+id+":withdraw", discordgo.DangerButton),
			button("Confirm roster", "fs:roster:"+id, discordgo.SecondaryButton),
		)}
	}
	return []discordgo.MessageComponent{row(
		button("Sign up", "fs:signup:"+id+":join", discordgo.PrimaryButton),
		button("Standby", "fs:signup:"+id+":standby", discordgo.SecondaryButton),
		button("Withdraw", "fs:signup:"+id+":withdraw", discordgo.DangerButton),
		button("Confirm roster", "fs:roster:"+id, discordgo.SecondaryButton),
	)}
}

var roleOrder = []string{"tank", "healer", "melee", "ranged"}
var roleHeading = map[string]string{"tank": "Tanks", "healer": "Healers", "melee": "Melee", "ranged": "Ranged"}

// signupLine is one roster entry: role icon, signup number, name.
func (b *Bot) signupLine(n int, s raids.Signup) string {
	role := s.Role
	if role == "" {
		role = "absent"
	}
	return fmt.Sprintf("%s `%2d` %s", b.icon(role), n, s.Character)
}

// rosterFields renders signups Raid-Helper style: a role-count line, then one inline
// field per class (three per row) listing role icon, signup number and name, then
// standby. Numbers are signup order, so people can see where they stand for a cap.
func (b *Bot) rosterFields(ups []raids.Signup, includeEmpty bool) (string, []*discordgo.MessageEmbedField, int, int) {
	byClass := map[string][]string{}
	roleCount := map[string]int{}
	var standby []string
	confirmed := 0
	for i, s := range ups {
		if s.Status == "standby" {
			standby = append(standby, b.signupLine(i+1, s))
			continue
		}
		confirmed++
		roleCount[s.Role]++
		class := people.NormalizeClass(s.Class)
		if class == "" {
			class = "Unknown"
		}
		byClass[class] = append(byClass[class], b.signupLine(i+1, s))
	}
	// Discord collapses runs of spaces; em-spaces and bullets keep the four counts apart.
	const gap = "\u2003\u2003•\u2003\u2003"
	counts := fmt.Sprintf("%s **%d** Tanks%s%s **%d** Healers%s%s **%d** Melee%s%s **%d** Ranged",
		b.icon("tank"), roleCount["tank"], gap, b.icon("healer"), roleCount["healer"], gap, b.icon("melee"), roleCount["melee"], gap, b.icon("ranged"), roleCount["ranged"])
	var fields []*discordgo.MessageEmbedField
	classes := append([]string{}, people.Classes...)
	if len(byClass["Unknown"]) > 0 {
		classes = append(classes, "Unknown")
	}
	for _, c := range classes {
		lines := byClass[c]
		if len(lines) == 0 && !includeEmpty {
			continue
		}
		v := strings.Join(lines, "\n")
		if v == "" {
			v = "-"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("%s %s (%d)", b.icon(c), c, len(lines)), Value: v, Inline: true})
	}
	// Pad to a multiple of three so the grid stays aligned.
	for len(fields)%3 != 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "​", Value: "​", Inline: true})
	}
	if len(standby) > 0 || includeEmpty {
		v := strings.Join(standby, "\n")
		if v == "" {
			v = "-"
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("Standby (%d)", len(standby)), Value: v})
	}
	return counts, fields, confirmed, len(standby)
}

// signupEmbed renders an event with its signups.
func (b *Bot) signupEmbed(ev raids.Event) *discordgo.MessageEmbed {
	ups, _ := b.Raids.Signups(ev.ID)
	counts, fields, confirmed, _ := b.rosterFields(ups, false)

	title := safeText(ev.Title)
	footer := "Sign up with a registered character. Register via /fairshare or the card in #get-started."
	if ev.RosterConfirmed() {
		title = "Roster confirmed: " + safeText(ev.Title)
		footer = "The roster is set. Withdraw if you cannot make it; officers can re-confirm."
	}
	repeat := ""
	switch ev.RepeatDays {
	case 7:
		repeat = "  ·  weekly"
	case 14:
		repeat = "  ·  every two weeks"
	}
	return &discordgo.MessageEmbed{
		Title:       title,
		Color:       0xC89C5C,
		Description: fmt.Sprintf("**%s**  ·  <t:%d:F> (<t:%d:R>)%s\n**%d / %d** signed up%s\n\n%s", safeText(ev.Instance), ev.StartsAt, ev.StartsAt, repeat, confirmed, ev.Cap, safeText(notesLine(ev.Notes)), counts),
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
	}
}

// refreshEvent re-renders the signup message after any change.
func (b *Bot) refreshEvent(eventID int64) {
	ev, err := b.Raids.Event(eventID)
	if err != nil || ev.ChannelID == "" {
		return
	}
	comps := signupButtons(ev)
	_, _ = b.edit(&discordgo.MessageEdit{Channel: ev.ChannelID, ID: ev.MessageID, Embeds: &[]*discordgo.MessageEmbed{b.signupEmbed(ev)}, Components: &comps})
}

func (b *Bot) handleSignup(i *discordgo.InteractionCreate, args []string) {
	if len(args) < 2 {
		return
	}
	eventID, _ := strconv.ParseInt(args[0], 10, 64)
	action := args[1]
	u := userOf(i)

	if action == "withdraw" {
		_ = b.Raids.Withdraw(eventID, u.ID)
		b.ephemeral(i, "You are withdrawn.")
		b.refreshEvent(eventID)
		return
	}
	// Signing up needs a registered character; nobody gets a person row just for
	// pressing the button. The display name refresh only happens for the registered.
	chars, _ := b.People.Characters(u.ID)
	if len(chars) == 0 {
		b.ephemeral(i, "Register a character first; it takes three clicks.", row(button("Register a character", "fs:register", discordgo.PrimaryButton)))
		return
	}
	_ = b.People.Ensure(u.ID, displayName(i))
	if len(chars) == 1 {
		b.chooseRole(i, eventID, chars[0], action == "standby", false)
		return
	}
	opts := make([]discordgo.SelectMenuOption, 0, len(chars))
	for _, c := range chars {
		label := c.Name
		if c.Class != "" {
			label += " · " + c.Class
		}
		opts = append(opts, discordgo.SelectMenuOption{Label: label, Value: c.Name})
	}
	b.ephemeral(i, "Which character?", row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:su-char:" + args[0] + ":" + action, Placeholder: "Character", Options: opts}))
}

func (b *Bot) pickSignupChar(i *discordgo.InteractionCreate, args []string) {
	vals := i.MessageComponentData().Values
	if len(args) < 2 || len(vals) == 0 {
		return
	}
	eventID, _ := strconv.ParseInt(args[0], 10, 64)
	c, err := b.People.Character(vals[0])
	if err != nil || c.PersonID != userOf(i).ID {
		b.updateClear(i, "That character is not registered to you.")
		return
	}
	b.chooseRole(i, eventID, c, args[1] == "standby", true)
}

// chooseRole either signs up straight away (standby, or a class with one role) or asks
// for the role. isUpdate says whether we are editing an existing ephemeral message.
func (b *Bot) chooseRole(i *discordgo.InteractionCreate, eventID int64, c people.Character, standby bool, isUpdate bool) {
	reply := func(content string, comps ...discordgo.MessageComponent) {
		if isUpdate {
			if len(comps) == 0 {
				b.updateClear(i, content)
			} else {
				b.update(i, content, comps...)
			}
		} else {
			b.ephemeral(i, content, comps...)
		}
	}
	roles := people.RolesFor(c.Class)
	if standby || len(roles) == 1 {
		role := ""
		if len(roles) == 1 {
			role = roles[0]
		}
		reply(b.doSignup(eventID, userOf(i), displayName(i), c.Name, role, standby))
		return
	}
	// Preselect the roles the player registered for.
	wanted := map[string]bool{}
	for _, r := range people.SplitRoles(c.Roles) {
		wanted[r] = true
	}
	reply(fmt.Sprintf("**%s** (%s): which role tonight?", c.Name, c.Class),
		row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:su-role:" + fmt.Sprint(eventID) + ":" + c.Name, Placeholder: "Role", Options: roleOptionsNoDefault(c.Class)}))
}

func roleOptionsNoDefault(class string) []discordgo.SelectMenuOption {
	return roleOptions(class, nil)
}

func (b *Bot) pickSignupRole(i *discordgo.InteractionCreate, args []string) {
	vals := i.MessageComponentData().Values
	if len(args) < 2 || len(vals) == 0 {
		return
	}
	eventID, _ := strconv.ParseInt(args[0], 10, 64)
	b.updateClear(i, b.doSignup(eventID, userOf(i), displayName(i), args[1], vals[0], false))
}

func (b *Bot) doSignup(eventID int64, u *discordgo.User, dn, character, role string, standby bool) string {
	status, err := b.Raids.Signup(eventID, u.ID, dn, character, role, standby)
	if err != nil {
		return "Could not sign up: " + err.Error()
	}
	b.refreshEvent(eventID)
	switch {
	case standby:
		return fmt.Sprintf("**%s** is on standby.", character)
	case status == "standby":
		return fmt.Sprintf("The raid is full; **%s** is on standby as %s.", character, people.RoleLabel[role])
	default:
		return fmt.Sprintf("**%s** signed up as %s.", character, people.RoleLabel[role])
	}
}

// ---------------------------------------------------------------- roster confirmation

type rosterPick struct {
	EventID  int64
	Selected map[string]bool // person id -> confirmed
	Page     int
}

const rosterPageSize = 25

func (b *Bot) startRoster(i *discordgo.InteractionCreate, args []string) {
	if !b.isOfficer(i) {
		b.ephemeral(i, "Officers only.")
		return
	}
	if len(args) < 1 {
		return
	}
	eventID, _ := strconv.ParseInt(args[0], 10, 64)
	ups, _ := b.Raids.Signups(eventID)
	if len(ups) == 0 {
		b.ephemeral(i, "Nobody has signed up yet.")
		return
	}
	st := &rosterPick{EventID: eventID, Selected: map[string]bool{}}
	for _, s := range ups {
		if s.Status == "confirmed" {
			st.Selected[s.PersonID] = true
		}
	}
	setPending("roster", userOf(i).ID, st)
	content, comps := b.rosterPage(st, ups)
	b.ephemeral(i, content, comps...)
}

func (b *Bot) rosterPage(st *rosterPick, ups []raids.Signup) (string, []discordgo.MessageComponent) {
	sort.SliceStable(ups, func(a, c int) bool { return ups[a].Character < ups[c].Character })
	pages := (len(ups) + rosterPageSize - 1) / rosterPageSize
	if pages < 1 {
		pages = 1
	}
	st.Page = clampPage(st.Page, pages)
	from := st.Page * rosterPageSize
	to := from + rosterPageSize
	if to > len(ups) {
		to = len(ups)
	}
	var opts []discordgo.SelectMenuOption
	for _, s := range ups[from:to] {
		label := s.Character + " (" + s.Class + ")"
		if s.Role != "" {
			label += " · " + people.RoleLabel[s.Role]
		}
		opts = append(opts, discordgo.SelectMenuOption{Label: truncate(label, 100), Value: s.PersonID, Default: st.Selected[s.PersonID]})
	}
	zero := 0
	comps := []discordgo.MessageComponent{row(discordgo.SelectMenu{
		MenuType: discordgo.StringSelectMenu, CustomID: fmt.Sprintf("fs:roster-pick:%d:%d", st.EventID, st.Page),
		Placeholder: "Confirmed raiders", MinValues: &zero, MaxValues: len(opts), Options: opts,
	})}
	var nav []discordgo.MessageComponent
	if st.Page > 0 {
		nav = append(nav, button("Previous page", fmt.Sprintf("fs:roster-page:%d:%d", st.EventID, st.Page-1), discordgo.SecondaryButton))
	}
	if st.Page < pages-1 {
		nav = append(nav, button("Next page", fmt.Sprintf("fs:roster-page:%d:%d", st.EventID, st.Page+1), discordgo.SecondaryButton))
	}
	nav = append(nav, button("Confirm roster", fmt.Sprintf("fs:roster-confirm:%d", st.EventID), discordgo.SuccessButton))
	comps = append(comps, row(nav...))
	content := fmt.Sprintf("Select everyone who is **in**; the rest go to standby. %d selected of %d signed up.", len(st.Selected), len(ups))
	if pages > 1 {
		content += fmt.Sprintf(" Page %d of %d.", st.Page+1, pages)
	}
	return content, comps
}

func (b *Bot) pickRoster(i *discordgo.InteractionCreate, args []string) {
	st, ok := getPending[*rosterPick]("roster", userOf(i).ID)
	if !ok || len(args) < 2 {
		b.updateClear(i, "That roster pick expired. Press Confirm roster again.")
		return
	}
	page, _ := strconv.Atoi(args[1])
	ups, _ := b.Raids.Signups(st.EventID)
	sort.SliceStable(ups, func(a, c int) bool { return ups[a].Character < ups[c].Character })
	page = clampPage(page, (len(ups)+rosterPageSize-1)/rosterPageSize)
	from := page * rosterPageSize
	to := from + rosterPageSize
	if to > len(ups) {
		to = len(ups)
	}
	if from < to {
		for _, s := range ups[from:to] {
			delete(st.Selected, s.PersonID)
		}
	}
	for _, v := range i.MessageComponentData().Values {
		st.Selected[v] = true
	}
	content, comps := b.rosterPage(st, ups)
	b.update(i, content, comps...)
}

func (b *Bot) rosterTurnPage(i *discordgo.InteractionCreate, args []string) {
	st, ok := getPending[*rosterPick]("roster", userOf(i).ID)
	if !ok || len(args) < 2 {
		b.updateClear(i, "That roster pick expired. Press Confirm roster again.")
		return
	}
	page, _ := strconv.Atoi(args[1])
	st.Page = clampPage(page, 1<<30) // rosterPage clamps the upper bound once it knows the count
	ups, _ := b.Raids.Signups(st.EventID)
	content, comps := b.rosterPage(st, ups)
	b.update(i, content, comps...)
}

// clampPage keeps a page index (which arrives in a custom id, so from the client) inside
// [0, pages-1].
func clampPage(page, pages int) int {
	if page < 0 {
		return 0
	}
	if pages > 0 && page > pages-1 {
		return pages - 1
	}
	return page
}

func (b *Bot) confirmRoster(i *discordgo.InteractionCreate, args []string) {
	u := userOf(i)
	st, ok := getPending[*rosterPick]("roster", u.ID)
	if !ok {
		b.updateClear(i, "That roster pick expired. Press Confirm roster again.")
		return
	}
	var confirmed []string
	for id := range st.Selected {
		confirmed = append(confirmed, id)
	}
	if err := b.Raids.ConfirmRoster(st.EventID, confirmed, u.ID); err != nil {
		b.updateClear(i, "Could not confirm: "+err.Error())
		return
	}
	delPending("roster", u.ID)
	ev, _ := b.Raids.Event(st.EventID)
	b.refreshEvent(ev.ID)
	ch := b.eventChannel(ev)
	if ch != "" {
		msg, err := b.send(ch, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{b.rosterEmbed(ev)}})
		if err == nil {
			_ = b.pinMessage(ch, msg.ID)
		}
	}
	b.logAction(u.ID, "roster_confirm", fmt.Sprintf("%s: %d confirmed", safeText(ev.Title), len(confirmed)))
	b.updateClear(i, fmt.Sprintf("Roster confirmed: %d in. Posted and pinned in <#%s>.", len(confirmed), ch))
}

// rosterEmbed is the pinned, final roster for an event.
func (b *Bot) rosterEmbed(ev raids.Event) *discordgo.MessageEmbed {
	ups, _ := b.Raids.Signups(ev.ID)
	counts, fields, confirmed, standby := b.rosterFields(ups, false)
	return &discordgo.MessageEmbed{
		Title:       "Roster: " + safeText(ev.Title),
		Color:       0x57C785,
		Description: fmt.Sprintf("**%s**  ·  <t:%d:F> (<t:%d:R>)\n**%d** confirmed, **%d** standby\n\n%s\n\nThe leader's addon shows this list with one-click invites.", safeText(ev.Instance), ev.StartsAt, ev.StartsAt, confirmed, standby, counts),
		Fields:      fields,
	}
}

func notesLine(notes string) string {
	if strings.TrimSpace(notes) == "" {
		return ""
	}
	return "\n" + strings.TrimSpace(notes)
}
