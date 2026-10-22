package people

import "github.com/JPWill/FairShare/backend/internal/store"

// AcceptTerms records that a member accepted the community rules and terms in
// #welcome. It creates the person row if needed, so acceptance can precede registration.
func (p *Service) AcceptTerms(discordID, displayName string) error {
	if err := p.Ensure(discordID, displayName); err != nil {
		return err
	}
	_, err := p.S.DB.Exec(`UPDATE person SET tos_accepted_at = ? WHERE discord_id = ? AND tos_accepted_at = 0`, store.Now(), discordID)
	return err
}

// Accepted reports whether the person has accepted the terms.
func (p *Service) Accepted(discordID string) bool {
	var at int64
	if err := p.S.DB.QueryRow(`SELECT tos_accepted_at FROM person WHERE discord_id = ?`, discordID).Scan(&at); err != nil {
		return false
	}
	return at > 0
}
