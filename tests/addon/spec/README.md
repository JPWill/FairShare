# spec/

One file per scenario. Each is a scripted ordered list of addon messages (sender, payload, server time) plus the expected end state: winner, price, flags, penalties.

Scenarios to cover, from the abuse model (auctioneer doctoring bids, share farming, debt and dash, bidding rings, shill bids, leader favouritism, ledger tampering, tampered addons, banned players returning):

- happy path, two bidders, second price
- single bidder pays floor
- off-spec single bidder pays discounted floor
- reveal does not match commit → rejected, flagged
- off-spec flag flipped between commit and reveal → rejected
- bid exceeds balance + credit → rejected
- bidder not in snapshot → rejected
- banned character in snapshot's banned list → rejected, leader warned
- commit with no reveal → penalty logged
- CLOSE committer list mismatch → flagged
- RESULT disagrees with recomputed winner → flagged
- tie → broken by sha256 of sorted salts
- ENCOUNTER_START mid-auction → paused, deadline extended
- late joiner receives ROW on roster update
- test auction (kind=test) runs fully and is marked test
