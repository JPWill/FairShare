package web

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/verify"
)

// Battle.net verification lives on the website because OAuth needs a public HTTPS
// callback. The bot hands the member a signed link; these two handlers do the rest and
// show a plain result page. Nothing else on the site is touched: no session, no cookie.

// verifyStart checks the signed state and sends the member to Discord.
func (s *Server) verifyStart(w http.ResponseWriter, r *http.Request) {
	if s.Verify == nil || s.Signer == nil {
		s.verifyPage(w, http.StatusNotFound, "Not set up", "Battle.net verification is not configured on this server yet. Ask an officer.", false)
		return
	}
	state := r.URL.Query().Get("s")
	if _, err := s.Signer.Parse(state, time.Now()); err != nil {
		s.verifyPage(w, http.StatusBadRequest, "That link does not work", "Open /fairshare in Discord and press Verify Battle.net again. Links last fifteen minutes. ("+err.Error()+")", false)
		return
	}
	http.Redirect(w, r, s.Verify.AuthorizeURL(state), http.StatusFound)
}

// verifyCallback is where Discord sends the member back.
func (s *Server) verifyCallback(w http.ResponseWriter, r *http.Request) {
	if s.Verify == nil || s.Signer == nil {
		s.verifyPage(w, http.StatusNotFound, "Not set up", "Battle.net verification is not configured on this server yet.", false)
		return
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		s.verifyPage(w, http.StatusOK, "Nothing changed", "You did not approve the request, so nothing was linked. You can try again from /fairshare in Discord whenever you like.", false)
		return
	}
	userID, err := s.Signer.Parse(q.Get("state"), time.Now())
	if err != nil {
		s.verifyPage(w, http.StatusBadRequest, "That link does not work", "Open /fairshare in Discord and press Verify Battle.net again. ("+err.Error()+")", false)
		return
	}
	res, err := s.Verify.Complete(r.Context(), q.Get("code"), userID)
	switch {
	case errors.Is(err, verify.ErrNoBattlenet):
		log.Printf("verify battlenet for %s: %v", userID, err)
		s.verifyPage(w, http.StatusOK, "No Battle.net on your Discord", "Discord has no verified Battle.net connection for your account. In Discord: User Settings → Connections → Battle.net, sign in there, then press Verify Battle.net again.", false)
		return
	case err != nil:
		log.Printf("verify battlenet for %s: %v", userID, err)
		s.verifyPage(w, http.StatusBadGateway, "Could not verify", "Discord did not confirm the link. "+err.Error()+". Try again in a minute.", false)
		return
	}
	if err := s.People.VerifyBattlenet(res.UserID, res.Tag); err != nil {
		if errors.Is(err, people.ErrBanned) {
			s.verifyPage(w, http.StatusOK, "Cannot link", "That Battle.net account cannot be linked here. Contact an officer.", false)
			return
		}
		log.Printf("store verified battlenet for %s: %v", userID, err)
		s.verifyPage(w, http.StatusInternalServerError, "Could not save", "Something went wrong saving the link. Try again shortly.", false)
		return
	}
	s.verifyPage(w, http.StatusOK, "Battle.net verified", "Your Battle.net account is linked and verified. You can close this tab; /fairshare in Discord shows it.", true)
}

func (s *Server) verifyPage(w http.ResponseWriter, status int, title, text string, ok bool) {
	s.render(w, status, "verify", title, "", map[string]any{"Heading": title, "Text": text, "OK": ok})
}
