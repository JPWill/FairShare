package replay

import "fmt"

// protocolLines extracts the comparable sequence: every `in` line whose type is a
// protocol type, as "sender body" (time ignored).
func protocolLines(tr *Transcript) []string {
	var out []string
	for _, l := range tr.Lines {
		if l.Dir != "in" {
			continue
		}
		m := Decode(l.Body)
		if !protocolTypes[m.Type] {
			continue
		}
		out = append(out, l.Sender+" "+l.Body)
	}
	return out
}

// Compare returns the differences between two transcripts' protocol sequences.
// An empty result means they match (§5).
func Compare(a, b *Transcript) []string {
	la, lb := protocolLines(a), protocolLines(b)
	var diffs []string
	n := len(la)
	if len(lb) < n {
		n = len(lb)
	}
	for i := 0; i < n; i++ {
		if la[i] != lb[i] {
			diffs = append(diffs, fmt.Sprintf("line %d: %s | %s", i+1, truncate(la[i]), truncate(lb[i])))
			if len(diffs) >= 10 {
				diffs = append(diffs, "...")
				return diffs
			}
		}
	}
	if len(la) != len(lb) {
		diffs = append(diffs, fmt.Sprintf("length differs: %d vs %d protocol lines", len(la), len(lb)))
	}
	return diffs
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
