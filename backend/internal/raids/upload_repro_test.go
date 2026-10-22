package raids

import (
	"encoding/base64"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// Regression: a sandbox test raid (snapshot issued by the bot, transcript exported by the
// addon after a test auction with virtual raiders) panicked the upload path. Every name in
// the fixture is a virtual raider; the checksum is of the snapshot text as embedded.
const reproSnapshotB64 = "RkFJUlNIQVJFIFNOQVBTSE9UIDEKcmFpZD0yCmlzc3VlZD0xNzkyNTUwMDYwCm9mZnNwZWM9MApmbG9vcjpERUZBVUxUPTEwCnJvdzpUZXN0ZXIJU2ltIFRlc3RlcgkwCTEwMApyb3c6SmFlbXoJSmFtZXMJMAkxMDAKcm93OkxhdGVjb21lcglTaW0gTGF0ZWNvbWVyCTAJMTAwCnJvdzpTaW1hCVNpbSBTaW1hCTAJMTAwCnJvdzpTaW1iYQlTaW0gU2ltYmEJMAkxMDAKcm93OlNpbWVvbglTaW0gU2ltZW9uCTAJMTAwCnJvdzpTaW1raW4JU2ltIFNpbWtpbgkwCTEwMApyb3c6U2ltb24JU2ltIFNpbW9uCTAJMTAwCnJvdzpTaW1vbmUJU2ltIFNpbW9uZQkwCTEwMApyb3c6U2ltcmFuCVNpbSBTaW1yYW4JMAkxMDAKcm93OlNpbXphCVNpbSBTaW16YQkwCTEwMA=="

const reproTranscriptB64 = "RkFJUlNIQVJFIFRSQU5TQ1JJUFQgMQpyYWlkPTIKc25hcHNob3Q9NzQ1NTk5YmNjODY5ZmFhMjM5Mjg2MzNhZTRhYjIwMGEzMzY3NjJlOWE0NTZkZDkzYmFiNjk2M2Q2NzMzNDkxNwpwbGF5ZXI9SmFlbXoKdmVyc2lvbj0wLjEuMAoxNzkyNTUwMjMwCWluCUphZW16CVNOQVBeMl43NDU1OTliY2M4NjlmYWEyMzkyODYzM2FlNGFiMjAwYTMzNjc2MmU5YTQ1NmRkOTNiYWI2OTYzZDY3MzM0OTE3XjE3OTI1NTAwNjBeMF5ERUZBVUxUPTEwXgoxNzkyNTUwMjMwCWluCUphZW16CVJPV1NeSmFlbXosSmFtZXMsMCwxMDBeU2ltYmEsU2ltIFNpbWJhLDAsMTAwXlNpbWVvbixTaW0gU2ltZW9uLDAsMTAwXlNpbW9uLFNpbSBTaW1vbiwwLDEwMF5TaW1vbmUsU2ltIFNpbW9uZSwwLDEwMF5TaW1yYW4sU2ltIFNpbXJhbiwwLDEwMAoxNzkyNTUwMjM4CWluCUphZW16CU9QRU5eMi0xXnRlc3RefGNmZmZmZmZmZnxIaXRlbTo2OTQ4Ojo6Ojo6OjoxOjo6Ojo6Ojo6fGhbSGVhcnRoc3RvbmVdfGh8cl4xXjE3OTI1NTAyNTgKMTc5MjU1MDIzOAlpbglKYWVteglDT01NSVReMi0xXmFkNDMyYzVkMTYwMjNiOGJjMDcxOTA2NWI2MjFmNDI4ZjZiMzcxMGQ0N2Q2ZjQwMDI2Y2Q2MWE5NTYxMTRiYjQKMTc5MjU1MDIzOQlpbglTaW1vbmUJQ09NTUlUXjItMV5lZWZlZTc5YTMwNWQ0NzcxNTIwYTIxZmI1Mzk4YmM4ZmQ1YTdkY2M1ZTkwZjMzNmExZDkwZTAwNGE5YjI0Y2E3CjE3OTI1NTAyMzkJaW4JU2ltb24JQ09NTUlUXjItMV44NWJhYmYxNGI5NDNlYzZiZDIwYmRmMmViNmIzYTBlNmYzOGI5NThmOGQwNzYyZTdmZDRmMTllZmQyNDMwNzVkCjE3OTI1NTAyMzkJaW4JU2ltcmFuCUNPTU1JVF4yLTFeNDBmMjljNjQ5OWIwYTY0ODRiYjMxNDM2MmZlZDZiZTgwMTU1YWI5ZGE2MmQ0MThmYmNhZTMyZDFjM2M3NjdkNwoxNzkyNTUwMjQwCWluCVNpbWVvbglDT01NSVReMi0xXjkxMDNjZjBjY2NhMGNjZDkwNTQ1MTIwOGI4N2ExMWM1ZmE0N2EzZDg2OGU2OGM1NTMxNzkxNzk5OGI5ZjUzZTYKMTc5MjU1MDI0MAlpbglTaW1iYQlDT01NSVReMi0xXmI2YjQ3OWI0ZTVhNjIxYTEyM2RjN2EwYmVjYzI2OTAzZGRhYTg0MWZhODZkYTFiNjE0ZDM5MWIzZGU3YzBhNzYKMTc5MjU1MDI1OAlpbglKYWVteglDTE9TRV4yLTFeSmFlbXosU2ltYmEsU2ltZW9uLFNpbW9uLFNpbW9uZSxTaW1yYW4KMTc5MjU1MDI1OAlpbglKYWVteglSRVZFQUxeMi0xXjFeMF42MTViYzY4N2JmZDA5NTA5YzBjNDAyMTZlYmYwYjg1NgoxNzkyNTUwMjU5CWluCVNpbW9uCVJFVkVBTF4yLTFeNTReMV44MmUyMjk0Njc1MDQ1MmYyYzUzY2E5MDk3YTYyZDc2YgoxNzkyNTUwMjU5CWluCVNpbWJhCVJFVkVBTF4yLTFeOTheMF5jNmIxNjMxNDY0MTllNTRmYWE0ZjQxNDVjN2M5MmQwNAoxNzkyNTUwMjU5CWluCVNpbXJhbglSRVZFQUxeMi0xXjk2XjBeNDQwZjhiN2FkMmE0ODk4NWQzZWQ0ZTk4MzJjOWZiNWIKMTc5MjU1MDI2MAlpbglTaW1lb24JUkVWRUFMXjItMV42MF4wXjA5MDU3YzQzNDRkZmM1ZjAwOGM3YjEwNjJiYzM4YzEzCjE3OTI1NTAyNjAJaW4JU2ltb25lCVJFVkVBTF4yLTFeMjdeMF4wZjBhMzZjM2ZlMmU5ZTc4OWI1MmYwYTliYWM3ZmI1ZgoxNzkyNTUwMjYyCWluCUphZW16CVJFU1VMVF4yLTFeU2ltYmFeOTY="

func TestUploadRealTestAuctionTranscript(t *testing.T) {
	r, p, _ := newServices(t)
	snapText, _ := base64.StdEncoding.DecodeString(reproSnapshotB64)
	trText, _ := base64.StdEncoding.DecodeString(reproTranscriptB64)
	if err := p.Ensure("L", "James"); err != nil {
		t.Fatal(err)
	}
	// Persons and characters as the sandbox would have them.
	for _, name := range append([]string{"Jaemz", "Tester"}, DevRaiderNames...) {
		pid := "dev:" + name
		if name == "Jaemz" {
			pid = "L"
		}
		_, _ = r.S.DB.Exec(`INSERT INTO person (discord_id, display_name, joined_at, free_passes) VALUES (?, ?, 1, 1) ON CONFLICT(discord_id) DO NOTHING`, pid, name)
		_, _ = r.S.DB.Exec(`INSERT INTO character (name, person_id, class, roles, registered_at) VALUES (?, ?, 'Warrior', 'melee', 1) ON CONFLICT(name) DO NOTHING`, name, pid)
	}
	cs := snapshot.Checksum(string(snapText))
	if _, err := r.S.DB.Exec(`INSERT INTO raid (id, event_id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status) VALUES (2, NULL, 'Dev test', 1, 'L', ?, ?, 'issued')`, string(snapText), cs); err != nil {
		t.Fatal(err)
	}
	out, err := r.Upload(2, "L", string(trText))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Logf("status %s: %s", out.Status, out.Message)
	if out.Report == nil {
		t.Fatal("no report")
	}
}
