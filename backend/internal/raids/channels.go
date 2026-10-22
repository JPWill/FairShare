package raids

// Each raid event series owns a Discord channel (#tuesday-7pm-mc). The bot creates it;
// the service only remembers the id and passes it to later occurrences.

// SetRaidChannel records the event's channel.
func (r *Service) SetRaidChannel(eventID int64, channelID string) error {
	_, err := r.S.DB.Exec(`UPDATE raid_event SET raid_channel_id = ? WHERE id = ?`, channelID, eventID)
	return err
}

// OpenEventsInChannel counts open events still using a raid channel, so the bot knows
// when a cancelled event leaves the channel orphaned.
func (r *Service) OpenEventsInChannel(channelID string) (int, error) {
	var n int
	err := r.S.DB.QueryRow(`SELECT COUNT(*) FROM raid_event WHERE raid_channel_id = ? AND status = 'open'`, channelID).Scan(&n)
	return n, err
}
