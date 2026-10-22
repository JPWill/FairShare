package web

import (
	"net/http"
	"strconv"
)

const perPage = 50

// Pager is the next/previous links under a long list.
type Pager struct {
	Page    int
	HasPrev bool
	HasNext bool
	Query   string // search text to carry along, already part of the form
	Sort    Sort   // column order to carry along
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	raids, err := s.raidRows("", 6, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	loot, err := s.sales("", nil, "r.date DESC, a.auction_id DESC", 8, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	boards, err := s.boards(5)
	if err != nil {
		s.fail(w, err)
		return
	}
	// The front page shows three boards; the full set is on Leaderboards.
	var front []Board
	for _, b := range boards {
		switch b.Title {
		case "Lifetime earned", "Raids attended", "Items won":
			front = append(front, b)
		}
	}
	s.render(w, http.StatusOK, "home", "", "home", map[string]any{
		"Stats": s.stats(), "Raids": dict("Rows", raids), "Loot": dict("Rows", loot), "Boards": front,
	})
}

func (s *Server) raids(w http.ResponseWriter, r *http.Request) {
	page := pageParam(r)
	srt, order := raidSort.read(r)
	rows, err := s.raidRows(order, perPage+1, (page-1)*perPage)
	if err != nil {
		s.fail(w, err)
		return
	}
	pager := Pager{Page: page, HasPrev: page > 1, HasNext: len(rows) > perPage, Sort: srt}
	if len(rows) > perPage {
		rows = rows[:perPage]
	}
	s.render(w, http.StatusOK, "raids", "Raids", "raids", map[string]any{"Raids": dict("Rows", rows, "Sort", srt, "Path", "/raids", "Q", ""), "Pager": pager})
}

func (s *Server) raid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return
	}
	d, err := s.raidDetail(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if d == nil {
		s.notFound(w, r)
		return
	}
	s.render(w, http.StatusOK, "raid", d.Title, "raids", d)
}

func (s *Server) loot(w http.ResponseWriter, r *http.Request) {
	page, q := pageParam(r), search(r)
	where, args := "", []any{}
	if q != "" {
		where = ` AND (a.item_link LIKE ? ESCAPE '\' OR COALESCE(a.transferred_to, a.winner_character) LIKE ? ESCAPE '\' OR p.display_name LIKE ? ESCAPE '\')`
		args = []any{likeArg(q), likeArg(q), likeArg(q)}
	}
	srt, order := lootSort.read(r)
	rows, err := s.sales(where, args, order, perPage+1, (page-1)*perPage)
	if err != nil {
		s.fail(w, err)
		return
	}
	pager := Pager{Page: page, HasPrev: page > 1, HasNext: len(rows) > perPage, Query: q, Sort: srt}
	if len(rows) > perPage {
		rows = rows[:perPage]
	}
	biggest, err := s.sales("", nil, "a.price DESC, r.date DESC", 5, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "loot", "Loot", "loot", map[string]any{"Loot": dict("Rows", rows, "Sort", srt, "Path", "/loot", "Q", q), "Pager": pager, "Q": q, "Biggest": biggest})
}

func (s *Server) leaderboards(w http.ResponseWriter, r *http.Request) {
	boards, err := s.boards(15)
	if err != nil {
		s.fail(w, err)
		return
	}
	perf, err := s.performers(15, 1)
	if err != nil {
		s.fail(w, err)
		return
	}
	biggest, err := s.sales("", nil, "a.price DESC, r.date DESC", 10, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "leaderboards", "Leaderboards", "leaderboards", map[string]any{"Boards": boards, "Performers": perf, "Biggest": biggest})
}

func (s *Server) players(w http.ResponseWriter, r *http.Request) {
	q := search(r)
	srt, order := playerSort.read(r)
	rows, err := s.directory(q, order)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "players", "Players", "players", map[string]any{"Players": rows, "Q": q, "Sort": srt})
}

func (s *Server) player(w http.ResponseWriter, r *http.Request) {
	p, err := s.profile(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if p == nil {
		s.notFound(w, r)
		return
	}
	s.render(w, http.StatusOK, "player", p.Player.Name, "players", p)
}
