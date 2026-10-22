package people

import (
	"strings"

	"github.com/JPWill/FairShare/backend/internal/store"
)

// VerifyBattlenet stores a tag that Discord confirmed (verify package) and marks it
// verified. The ban check is the same as for a typed tag; the difference is that this
// tag is Blizzard's word, not the member's.
func (p *Service) VerifyBattlenet(discordID, tag string) error {
	tag = strings.TrimSpace(tag)
	if p.IsBannedIdentity != nil && p.IsBannedIdentity("battlenet", strings.ToLower(tag)) {
		return ErrBanned
	}
	var name string
	if err := p.S.DB.QueryRow(`SELECT display_name FROM person WHERE discord_id = ?`, discordID).Scan(&name); err != nil {
		name = "Member"
	}
	if err := p.Ensure(discordID, name); err != nil {
		return err
	}
	_, err := p.S.DB.Exec(`UPDATE person SET battlenet_tag = ?, battlenet_verified_at = ? WHERE discord_id = ?`, tag, store.Now(), discordID)
	return err
}
