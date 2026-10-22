package web

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// Sorting is server side: the site ships no JavaScript, so a column header is a link to
// the same page with ?sort=<key>&dir=<asc|desc>. Keys are whitelisted per table and
// mapped to SQL here; nothing from the query string reaches the database as text.

// Sort is the current sort of a table, as the template sees it.
type Sort struct {
	Key string
	Dir string // "asc" or "desc"
}

// sortSpec is one table's allowed sort keys.
type sortSpec struct {
	cols       map[string]string // key -> SQL expression (or ordinal)
	defKey     string
	defDir     string
	tiebreak   string // appended after the chosen column
	textKeys   map[string]bool
	defaultDir map[string]string // per key, when first clicked (text ascends, numbers descend)
}

// read picks the sort from the request, falling back to the default for unknown values,
// and returns it with the ORDER BY body to use. Nothing from the request is kept as
// text: the key only selects a whitelisted column, and the direction is re-issued as
// one of two literals, so the ORDER BY is assembled from constants alone (which is also
// what lets a static scanner see that it is).
func (sp sortSpec) read(r *http.Request) (Sort, string) {
	key := r.URL.Query().Get("sort")
	col, ok := sp.cols[key]
	if !ok {
		key, col = sp.defKey, sp.cols[sp.defKey]
	}
	var dir string
	switch {
	case !ok:
		dir = sp.defDir
	case r.URL.Query().Get("dir") == "asc":
		dir = "asc"
	case r.URL.Query().Get("dir") == "desc":
		dir = "desc"
	default:
		dir = sp.firstDir(key)
	}
	order := col + " " + strings.ToUpper(dir)
	if sp.tiebreak != "" {
		order += ", " + sp.tiebreak
	}
	return Sort{Key: key, Dir: dir}, order
}

func (sp sortSpec) firstDir(key string) string {
	if d, ok := sp.defaultDir[key]; ok {
		return d
	}
	return "desc"
}

var lootSort = sortSpec{
	cols: map[string]string{
		"date":      "r.date",
		"raid":      "COALESCE(NULLIF(e.title, ''), r.instance) COLLATE NOCASE",
		"item":      "substr(a.item_link, instr(a.item_link, '[') + 1) COLLATE NOCASE",
		"player":    "COALESCE(p.display_name, owner) COLLATE NOCASE",
		"character": "owner COLLATE NOCASE",
		"price":     "a.price",
	},
	defKey: "date", defDir: "desc",
	tiebreak:   "r.date DESC, a.raid_id DESC, a.auction_id DESC",
	defaultDir: map[string]string{"raid": "asc", "item": "asc", "player": "asc"},
}

var raidSort = sortSpec{
	cols: map[string]string{
		"date":    "r.date",
		"raid":    "3 COLLATE NOCASE",
		"leader":  "5 COLLATE NOCASE",
		"status":  "r.status",
		"raiders": "8",
		"bosses":  "9",
		"items":   "10",
		"spent":   "11",
	},
	defKey: "date", defDir: "desc",
	tiebreak:   "r.date DESC, r.id DESC",
	defaultDir: map[string]string{"raid": "asc", "leader": "asc", "status": "asc"},
}

var playerSort = sortSpec{
	cols: map[string]string{
		"player":  "p.display_name COLLATE NOCASE",
		"raids":   "p.raids_attended",
		"earned":  "8",
		"kills":   "p.lifetime_shares",
		"balance": "p.balance",
	},
	defKey: "player", defDir: "asc",
	tiebreak:   "p.display_name COLLATE NOCASE",
	defaultDir: map[string]string{"player": "asc"},
}

// sortURL is the header link for key on a table currently sorted by cur: clicking the
// current column flips it, any other column starts at its natural direction.
func sortURL(path, key, firstDir string, cur Sort, q string) string {
	dir := firstDir
	if cur.Key == key {
		if cur.Dir == "asc" {
			dir = "desc"
		} else {
			dir = "asc"
		}
	}
	v := url.Values{}
	v.Set("sort", key)
	v.Set("dir", dir)
	if q != "" {
		v.Set("q", q)
	}
	return path + "?" + v.Encode()
}

// sortClass marks the header of the current sort column so the stylesheet can show an arrow.
func sortClass(key string, cur Sort) string {
	if cur.Key == key {
		return "sort on " + cur.Dir
	}
	return "sort"
}

// sortQuery is what the pager appends so the next page keeps the order.
func sortQuery(s Sort) template.URL {
	if s.Key == "" {
		return ""
	}
	return template.URL("&sort=" + url.QueryEscape(s.Key) + "&dir=" + s.Dir)
}

// dict builds a map for passing several values to a nested template.
func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}
