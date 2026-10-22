// Package bans issues and lifts bans. A ban is recorded against the person and every
// identity ever linked to them (Discord id, Battle.net tag, every character), so the
// same player cannot return on a fresh Discord account.
package bans

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Service is the bans service.
type Service struct {
	S      *store.Store
	People *people.Service
}

// Ban is one ban record.
type Ban struct {
	ID         int64
	PersonID   string
	Reason     string
	IssuedBy   string
	IssuedAt   int64
	LiftedAt   int64
	Identities []Identity
}

// Identity is one banned identity.
type Identity struct {
	Kind  string // discord | battlenet | character
	Value string
}

// Issue records a ban for the person and snapshots their identities.
func (b *Service) Issue(discordID, reason, by string) (Ban, error) {
	_ = b.People.Ensure(discordID, "unknown")
	p, err := b.People.Get(discordID)
	if err != nil {
		return Ban{}, err
	}
	chars, err := b.People.Characters(discordID)
	if err != nil {
		return Ban{}, err
	}
	ids := []Identity{{Kind: "discord", Value: discordID}}
	if p.BattlenetTag != "" {
		ids = append(ids, Identity{Kind: "battlenet", Value: strings.ToLower(p.BattlenetTag)})
	}
	for _, c := range chars {
		ids = append(ids, Identity{Kind: "character", Value: c.Name})
	}
	var ban Ban
	err = b.S.Tx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO ban (person_id, reason, issued_by, issued_at) VALUES (?, ?, ?, ?)`, discordID, reason, by, store.Now())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		for _, i := range ids {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO ban_identity (ban_id, kind, value) VALUES (?, ?, ?)`, id, i.Kind, i.Value); err != nil {
				return err
			}
		}
		ban = Ban{ID: id, PersonID: discordID, Reason: reason, IssuedBy: by, IssuedAt: store.Now(), Identities: ids}
		return nil
	})
	if err != nil {
		return Ban{}, err
	}
	// The ban in the database is the source of truth whether or not Discord accepts the
	// server ban afterwards (the bot reports that separately), so the person's API
	// tokens and addon key go now: a banned leader's sync client must stop working.
	if err := b.People.RevokeAccess(discordID); err != nil {
		b.S.Audit(by, "ban_revoke_failed", fmt.Sprintf("person %s: %v", discordID, err))
	}
	b.S.Audit(by, "ban", fmt.Sprintf("person %s (%s): %s; identities %d", discordID, p.DisplayName, reason, len(ids)))
	return ban, nil
}

// Lift marks a ban lifted; its identities stop matching.
func (b *Service) Lift(banID int64, by, reason string) error {
	res, err := b.S.DB.Exec(`UPDATE ban SET lifted_at = ?, lifted_by = ?, lift_reason = ? WHERE id = ? AND lifted_at IS NULL`, store.Now(), by, reason, banID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such active ban")
	}
	b.S.Audit(by, "unban", fmt.Sprintf("ban %d: %s", banID, reason))
	return nil
}

// IsBannedIdentity reports whether an identity belongs to any active ban.
func (b *Service) IsBannedIdentity(kind, value string) bool {
	if kind == "battlenet" {
		value = strings.ToLower(value)
	}
	var n int
	_ = b.S.DB.QueryRow(`SELECT COUNT(*) FROM ban_identity bi JOIN ban ON ban.id = bi.ban_id WHERE ban.lifted_at IS NULL AND bi.kind = ? AND bi.value = ?`, kind, value).Scan(&n)
	return n > 0
}

// IsBannedPerson reports whether the person has an active ban.
func (b *Service) IsBannedPerson(discordID string) bool {
	var n int
	_ = b.S.DB.QueryRow(`SELECT COUNT(*) FROM ban WHERE person_id = ? AND lifted_at IS NULL`, discordID).Scan(&n)
	return n > 0
}

// Active lists active bans.
func (b *Service) Active() ([]Ban, error) {
	rows, err := b.S.DB.Query(`SELECT id, person_id, reason, issued_by, issued_at FROM ban WHERE lifted_at IS NULL ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ban
	for rows.Next() {
		var x Ban
		if err := rows.Scan(&x.ID, &x.PersonID, &x.Reason, &x.IssuedBy, &x.IssuedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// BannedCharacters lists every character name under an active ban, plus the current
// characters of actively banned persons (in case one was registered after the ban).
func (b *Service) BannedCharacters() ([]string, error) {
	rows, err := b.S.DB.Query(`
		SELECT DISTINCT value FROM ban_identity bi JOIN ban ON ban.id = bi.ban_id WHERE ban.lifted_at IS NULL AND bi.kind = 'character'
		UNION
		SELECT DISTINCT c.name FROM character c JOIN ban ON ban.person_id = c.person_id WHERE ban.lifted_at IS NULL
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
