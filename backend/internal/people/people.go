// Package people manages persons (Discord identities), their characters, Battle.net
// tags, credit, ledger views, and API tokens.
package people

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JPWill/FairShare/backend/internal/store"
)

// Service is the people service.
type Service struct {
	S *store.Store
	// IsBannedIdentity is injected by the bans package to avoid an import cycle.
	IsBannedIdentity func(kind, value string) bool
}

// Person is one Discord account's standing.
type Person struct {
	DiscordID           string
	DisplayName         string
	JoinedAt            int64
	RaidsAttended       int64
	LifetimeShares      int64
	Balance             int64
	FreePasses          int64
	BattlenetTag        string
	BattlenetVerifiedAt int64 // 0 unless Discord confirmed the tag
}

// Character is one registered character.
type Character struct {
	Name         string // bare in-game name, docs/protocol.md §1
	PersonID     string
	Class        string
	Roles        string // comma-joined roles the player wants to play on it
	RegisteredAt int64
}

// LedgerLine is one ledger row for display.
type LedgerLine struct {
	ID        int64
	RaidID    int64
	Delta     int64
	Reason    string
	AuctionID string
	Note      string
	Character string // the character it was earned on, "" for officer lines
	CreatedAt int64
}

var (
	ErrAccountTooNew   = errors.New("discord account is too new to register")
	ErrBanned          = errors.New("this identity is banned")
	ErrCharacterTaken  = errors.New("that character is registered to someone else")
	ErrBadName         = errors.New("character name must be one or two words of letters, like Thrall or Thrall Doomhammer")
	ErrNoCharacters    = errors.New("register a character first")
	ErrInvalidToken    = errors.New("invalid token")
	ErrNotRegistered   = errors.New("not registered")
	errNoSuchCharacter = errors.New("no such character")
)

// DiscordAccountCreated derives the account creation time from a snowflake id.
func DiscordAccountCreated(discordID string) time.Time {
	id, err := strconv.ParseUint(discordID, 10, 64)
	if err != nil {
		return time.Time{}
	}
	ms := int64(id>>22) + 1420070400000
	return time.UnixMilli(ms)
}

// Ensure creates the person row if missing and refreshes the display name.
func (p *Service) Ensure(discordID, displayName string) error {
	_, err := p.S.DB.Exec(`INSERT INTO person (discord_id, display_name, joined_at, free_passes) VALUES (?, ?, ?, ?)
		ON CONFLICT(discord_id) DO UPDATE SET display_name = excluded.display_name`,
		discordID, cleanName(displayName), store.Now(), p.S.ConfigInt("freePasses"))
	return err
}

// cleanName makes a Discord display name safe to print anywhere the bot writes: no
// tabs or newlines (they break tables and the snapshot's row format), no commas, no
// backticks (they would close a code fence), and no mass-mention words.
func cleanName(s string) string {
	s = strings.NewReplacer("\t", " ", "\n", " ", "\r", "", ",", " ", "`", "'",
		"@everyone", "everyone", "@here", "here").Replace(strings.TrimSpace(s))
	return strings.TrimSpace(s)
}

// Register links a character to a person after the age and ban checks. class must be
// one of Classes (or empty); roles are filtered to what the class can play.
func (p *Service) Register(discordID, displayName string, accountCreated time.Time, character, class string, roles ...string) (string, error) {
	name, err := NormalizeCharacter(character)
	if err != nil {
		return "", err
	}
	class = NormalizeClass(class)
	roleList := NormalizeRoles(class, roles)
	minDays := p.S.ConfigInt("minAccountAgeDays")
	if !accountCreated.IsZero() && time.Since(accountCreated) < time.Duration(minDays)*24*time.Hour {
		return "", ErrAccountTooNew
	}
	if p.IsBannedIdentity != nil {
		if p.IsBannedIdentity("discord", discordID) || p.IsBannedIdentity("character", name) {
			return "", ErrBanned
		}
	}
	if err := p.Ensure(discordID, displayName); err != nil {
		return "", err
	}
	var owner string
	err = p.S.DB.QueryRow(`SELECT person_id FROM character WHERE name = ?`, name).Scan(&owner)
	switch {
	case err == nil && owner != discordID:
		return "", ErrCharacterTaken
	case err == nil:
		_, err = p.S.DB.Exec(`UPDATE character SET class = ?, roles = ? WHERE name = ?`, class, roleList, name)
		return name, err
	case errors.Is(err, sql.ErrNoRows):
		_, err = p.S.DB.Exec(`INSERT INTO character (name, person_id, class, roles, registered_at) VALUES (?, ?, ?, ?, ?)`, name, discordID, class, roleList, store.Now())
		return name, err
	default:
		return "", err
	}
}

// LinkBattlenet stores the Battle.net tag after the ban check.
func (p *Service) LinkBattlenet(discordID, displayName, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" || !strings.Contains(tag, "#") {
		return fmt.Errorf("battle tag must look like Name#1234")
	}
	if p.IsBannedIdentity != nil && p.IsBannedIdentity("battlenet", strings.ToLower(tag)) {
		return ErrBanned
	}
	if err := p.Ensure(discordID, displayName); err != nil {
		return err
	}
	_, err := p.S.DB.Exec(`UPDATE person SET battlenet_tag = ? WHERE discord_id = ?`, tag, discordID)
	return err
}

// Get returns a person.
func (p *Service) Get(discordID string) (Person, error) {
	var x Person
	var tag sql.NullString
	err := p.S.DB.QueryRow(`SELECT discord_id, display_name, joined_at, raids_attended, lifetime_shares, balance, free_passes, battlenet_tag, battlenet_verified_at FROM person WHERE discord_id = ?`, discordID).
		Scan(&x.DiscordID, &x.DisplayName, &x.JoinedAt, &x.RaidsAttended, &x.LifetimeShares, &x.Balance, &x.FreePasses, &tag, &x.BattlenetVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotRegistered
	}
	x.BattlenetTag = tag.String
	return x, err
}

// Characters lists a person's characters, oldest first.
func (p *Service) Characters(discordID string) ([]Character, error) {
	rows, err := p.S.DB.Query(`SELECT name, person_id, COALESCE(class, ''), COALESCE(roles, ''), registered_at FROM character WHERE person_id = ? ORDER BY registered_at`, discordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		var c Character
		if err := rows.Scan(&c.Name, &c.PersonID, &c.Class, &c.Roles, &c.RegisteredAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AllCharacters lists every registered character with its owner.
func (p *Service) AllCharacters() ([]Character, error) {
	rows, err := p.S.DB.Query(`SELECT name, person_id, COALESCE(class, ''), COALESCE(roles, ''), registered_at FROM character ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		var c Character
		if err := rows.Scan(&c.Name, &c.PersonID, &c.Class, &c.Roles, &c.RegisteredAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Character loads one character by name.
func (p *Service) Character(name string) (Character, error) {
	var c Character
	err := p.S.DB.QueryRow(`SELECT name, person_id, COALESCE(class, ''), COALESCE(roles, ''), registered_at FROM character WHERE name = ?`, name).
		Scan(&c.Name, &c.PersonID, &c.Class, &c.Roles, &c.RegisteredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, errNoSuchCharacter
	}
	return c, err
}

// CharactersOf lists the names of a person's characters (oldest first).
func (p *Service) CharactersOf(discordID string) ([]string, error) {
	chars, err := p.Characters(discordID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(chars))
	for _, c := range chars {
		out = append(out, c.Name)
	}
	return out, nil
}

// ---------------------------------------------------------------- addon keys

// IssueAddonKey gives the person a fresh addon key (32 lowercase hex chars), replacing
// any previous one, and returns it. The addon signs COMMIT, REVEAL and ACK lines with it
// (docs/protocol.md §11); the previous key stops verifying at once.
func (p *Service) IssueAddonKey(discordID string) (string, error) {
	if _, err := p.Get(discordID); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	if _, err := p.S.DB.Exec(`UPDATE person SET addon_key = ? WHERE discord_id = ?`, key, discordID); err != nil {
		return "", err
	}
	return key, nil
}

// AddonKey returns the person's current addon key, or "" when none was issued.
func (p *Service) AddonKey(discordID string) (string, error) {
	var key string
	err := p.S.DB.QueryRow(`SELECT addon_key FROM person WHERE discord_id = ?`, discordID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotRegistered
	}
	return key, err
}

// CharacterKeys maps every registered character whose owner has an addon key to that
// key, for transcript replay.
func (p *Service) CharacterKeys() (map[string]string, error) {
	rows, err := p.S.DB.Query(`SELECT c.name, p.addon_key FROM character c JOIN person p ON p.discord_id = c.person_id WHERE p.addon_key <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, key string
		if err := rows.Scan(&name, &key); err != nil {
			return nil, err
		}
		out[name] = key
	}
	return out, rows.Err()
}

// PersonByCharacter resolves a character name to its owner.
func (p *Service) PersonByCharacter(name string) (string, error) {
	var id string
	err := p.S.DB.QueryRow(`SELECT person_id FROM character WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNoSuchCharacter
	}
	return id, err
}

// Credit is min(creditCap, newcomerCredit + creditPerRaid × raids_attended). With
// creditNeedsBattlenet on, a member whose Battle.net tag is not verified stays at the
// newcomer level however many raids they attend: debt-and-dash is only worth doing with
// credit, and a verified tag is what makes a ban follow the person.
func (p *Service) Credit(x Person) int64 {
	if strings.EqualFold(p.S.ConfigString("creditNeedsBattlenet"), "true") && x.BattlenetVerifiedAt == 0 {
		return p.S.ConfigInt("newcomerCredit")
	}
	c := p.S.ConfigInt("newcomerCredit") + p.S.ConfigInt("creditPerRaid")*x.RaidsAttended
	if cap := p.S.ConfigInt("creditCap"); c > cap {
		c = cap
	}
	return c
}

// Ledger returns the newest entries for a person.
func (p *Service) Ledger(discordID string, limit int) ([]LedgerLine, error) {
	rows, err := p.S.DB.Query(`SELECT id, COALESCE(raid_id, 0), delta, reason, COALESCE(auction_id, ''), COALESCE(note, ''), COALESCE(character, ''), created_at FROM ledger WHERE person_id = ? ORDER BY id DESC LIMIT ?`, discordID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerLine
	for rows.Next() {
		var l LedgerLine
		if err := rows.Scan(&l.ID, &l.RaidID, &l.Delta, &l.Reason, &l.AuctionID, &l.Note, &l.Character, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Standings lists everyone by lifetime shares, then balance.
func (p *Service) Standings(limit int) ([]Person, error) {
	rows, err := p.S.DB.Query(`SELECT discord_id, display_name, joined_at, raids_attended, lifetime_shares, balance, free_passes, COALESCE(battlenet_tag, '') FROM person ORDER BY lifetime_shares DESC, balance DESC, display_name LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var x Person
		if err := rows.Scan(&x.DiscordID, &x.DisplayName, &x.JoinedAt, &x.RaidsAttended, &x.LifetimeShares, &x.Balance, &x.FreePasses, &x.BattlenetTag); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- API tokens

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// IssueToken creates a new API token for a person, replacing any previous one, and
// returns it in the clear (the only time it is visible). A token lets its holder act
// as the person on the HTTP API (read their current snapshot, upload transcripts), so
// only someone with a registered character may hold one: a bare Discord account that
// has never registered has nothing to sync and no business holding API credentials.
func (p *Service) IssueToken(discordID string) (string, error) {
	if _, err := p.Get(discordID); err != nil {
		return "", err
	}
	chars, err := p.Characters(discordID)
	if err != nil {
		return "", err
	}
	if len(chars) == 0 {
		return "", ErrNoCharacters
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := "fs_" + hex.EncodeToString(buf)
	if _, err := p.S.DB.Exec(`DELETE FROM api_token WHERE person_id = ?`, discordID); err != nil {
		return "", err
	}
	if _, err := p.S.DB.Exec(`INSERT INTO api_token (hash, person_id, created_at) VALUES (?, ?, ?)`, hashToken(tok), discordID, store.Now()); err != nil {
		return "", err
	}
	return tok, nil
}

// RevokeAccess removes every credential the person holds: their API tokens, and their
// addon signing key. Called when a person is banned so a banned leader's sync client
// stops working at once.
func (p *Service) RevokeAccess(discordID string) error {
	if _, err := p.S.DB.Exec(`DELETE FROM api_token WHERE person_id = ?`, discordID); err != nil {
		return err
	}
	_, err := p.S.DB.Exec(`UPDATE person SET addon_key = '' WHERE discord_id = ?`, discordID)
	return err
}

// Authenticate resolves a bearer token to its person.
func (p *Service) Authenticate(tok string) (Person, error) {
	var id string
	err := p.S.DB.QueryRow(`SELECT person_id FROM api_token WHERE hash = ?`, hashToken(tok)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrInvalidToken
	}
	if err != nil {
		return Person{}, err
	}
	_, _ = p.S.DB.Exec(`UPDATE api_token SET last_used_at = ? WHERE hash = ?`, store.Now(), hashToken(tok))
	return p.Get(id)
}
