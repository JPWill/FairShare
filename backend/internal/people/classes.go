package people

import (
	"sort"
	"strings"
	"unicode"
)

// Classes are the nine WoW: Forever classes, in the order the game lists them.
var Classes = []string{"Warrior", "Paladin", "Hunter", "Rogue", "Priest", "Shaman", "Mage", "Warlock", "Druid"}

// Roles a character can sign up as.
var Roles = []string{"tank", "healer", "melee", "ranged"}

// RoleLabel is the display name for a role key.
var RoleLabel = map[string]string{"tank": "Tank", "healer": "Healer", "melee": "Melee DPS", "ranged": "Ranged DPS"}

var classRoles = map[string][]string{
	"Warrior": {"tank", "melee"},
	"Paladin": {"tank", "healer", "melee"},
	"Hunter":  {"ranged"},
	"Rogue":   {"melee"},
	"Priest":  {"healer", "ranged"},
	"Shaman":  {"healer", "melee", "ranged"},
	"Mage":    {"ranged"},
	"Warlock": {"ranged"},
	"Druid":   {"tank", "healer", "melee", "ranged"},
}

// NormalizeClass maps any casing of a class name to its canonical form, or "".
func NormalizeClass(s string) string {
	s = strings.TrimSpace(s)
	for _, c := range Classes {
		if strings.EqualFold(c, s) {
			return c
		}
	}
	return ""
}

// RolesFor lists the roles a class can play, in display order.
func RolesFor(class string) []string {
	return append([]string(nil), classRoles[NormalizeClass(class)]...)
}

// RoleAllowed reports whether a class can play a role.
func RoleAllowed(class, role string) bool {
	for _, r := range RolesFor(class) {
		if r == role {
			return true
		}
	}
	return false
}

// NormalizeRoles keeps the valid roles for the class, deduplicated, in display order,
// and returns them comma-joined for storage.
func NormalizeRoles(class string, roles []string) string {
	keep := map[string]bool{}
	for _, r := range roles {
		r = strings.ToLower(strings.TrimSpace(r))
		if RoleAllowed(class, r) {
			keep[r] = true
		}
	}
	var out []string
	for _, r := range Roles {
		if keep[r] {
			out = append(out, r)
		}
	}
	return strings.Join(out, ",")
}

// SplitRoles parses a stored roles string.
func SplitRoles(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	sort.SliceStable(parts, func(i, j int) bool { return roleIndex(parts[i]) < roleIndex(parts[j]) })
	return parts
}

func roleIndex(r string) int {
	for i, x := range Roles {
		if x == r {
			return i
		}
	}
	return len(Roles)
}

// NormalizeCharacter canonicalizes a typed character name per docs/protocol.md §1:
// trimmed, internal whitespace collapsed, Title Case per word, one or two words of
// letters only. A trailing "-Realm" (old habit) is dropped. Returns ErrBadName otherwise.
func NormalizeCharacter(s string) (string, error) {
	s = strings.TrimSpace(s)
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		s = strings.TrimSpace(s[:dash])
	}
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 2 {
		return "", ErrBadName
	}
	for i, w := range words {
		runes := []rune(w)
		if len(runes) < 2 || len(runes) > 16 {
			return "", ErrBadName
		}
		for _, r := range runes {
			if !unicode.IsLetter(r) {
				return "", ErrBadName
			}
		}
		runes[0] = unicode.ToUpper(runes[0])
		for k := 1; k < len(runes); k++ {
			runes[k] = unicode.ToLower(runes[k])
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " "), nil
}
