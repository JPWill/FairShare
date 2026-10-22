// Package verify proves a member's Battle.net tag through Discord. Members link
// Battle.net inside Discord's own settings, where Blizzard authenticates them; the bot
// then sends them through Discord's OAuth login on the website with the `connections`
// scope and reads the linked tag back together with Discord's verified flag. No tag is
// typed, so a banned person cannot invent one, and the bot never sees a Blizzard login.
//
// The flow: the bot builds a link to /verify/battlenet?s=<state> where state is this
// package's signed, short-lived token naming the Discord user who clicked. The website
// checks it, redirects to Discord, and on the callback exchanges the code, reads the
// user and their connections, confirms the user matches the state, and stores the tag.
package verify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DiscordAPI is the Discord REST base; tests point it at a stub.
const DiscordAPI = "https://discord.com/api/v10"

// StateTTL is how long a verification link stays valid after the button press.
const StateTTL = 15 * time.Minute

// Signer makes and checks the state tokens that bind a callback to the member who asked.
type Signer struct{ key []byte }

// NewSigner derives the signing key from the OAuth client secret, so no extra secret
// needs configuring and the bot and website agree without sharing state.
func NewSigner(clientSecret string) *Signer {
	sum := sha256.Sum256([]byte("fairshare-verify|" + clientSecret))
	return &Signer{key: sum[:]}
}

// State is a token for userID that expires StateTTL after now.
func (s *Signer) State(userID string, now time.Time) string {
	payload := userID + "|" + strconv.FormatInt(now.Add(StateTTL).Unix(), 10)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Parse returns the user id a state names, or an error when it is forged or expired.
func (s *Signer) Parse(state string, now time.Time) (string, error) {
	body, sig, ok := strings.Cut(state, ".")
	if !ok {
		return "", errors.New("malformed state")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", errors.New("malformed state")
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return "", errors.New("malformed state")
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), want) {
		return "", errors.New("state signature does not match")
	}
	userID, expText, ok := strings.Cut(string(payload), "|")
	exp, perr := strconv.ParseInt(expText, 10, 64)
	if !ok || perr != nil || userID == "" {
		return "", errors.New("malformed state")
	}
	if now.Unix() > exp {
		return "", errors.New("this link has expired; press Verify Battle.net again")
	}
	return userID, nil
}

// Client talks to Discord's OAuth endpoints.
type Client struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	API          string // DiscordAPI unless a test overrides it
	HTTP         *http.Client
}

func (c *Client) api() string {
	if c.API != "" {
		return c.API
	}
	return DiscordAPI
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// AuthorizeURL is where the member is sent to approve the identify and connections scopes.
func (c *Client) AuthorizeURL(state string) string {
	v := url.Values{}
	v.Set("client_id", c.ClientID)
	v.Set("response_type", "code")
	v.Set("redirect_uri", c.RedirectURI)
	v.Set("scope", "identify connections")
	v.Set("state", state)
	v.Set("prompt", "consent")
	return "https://discord.com/oauth2/authorize?" + v.Encode()
}

// Connection is one third-party account linked to a Discord user.
type Connection struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
}

// Result is what a completed verification found.
type Result struct {
	UserID string
	Tag    string
}

// Complete exchanges the code and reads the user's verified Battle.net tag. It refuses
// when the logged-in Discord user is not the one the state named.
func (c *Client) Complete(ctx context.Context, code, expectUserID string) (Result, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURI)
	req, err := http.NewRequestWithContext(ctx, "POST", c.api()+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}
	if err := c.do(req, &tok); err != nil {
		return Result{}, fmt.Errorf("token exchange: %w", err)
	}
	if tok.AccessToken == "" {
		return Result{}, errors.New("token exchange: no access token")
	}
	if !strings.Contains(tok.Scope, "connections") {
		return Result{}, errors.New("the connections permission was not granted")
	}
	bearer := func(path string) (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, "GET", c.api()+path, nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		return r, nil
	}
	var me struct {
		ID string `json:"id"`
	}
	req, err = bearer("/users/@me")
	if err != nil {
		return Result{}, err
	}
	if err := c.do(req, &me); err != nil {
		return Result{}, fmt.Errorf("read user: %w", err)
	}
	if me.ID != expectUserID {
		return Result{}, errors.New("you are logged in to Discord as a different account than the one that pressed the button")
	}
	var conns []Connection
	req, err = bearer("/users/@me/connections")
	if err != nil {
		return Result{}, err
	}
	if err := c.do(req, &conns); err != nil {
		return Result{}, fmt.Errorf("read connections: %w", err)
	}
	tag, ok := BattlenetTag(conns)
	if !ok {
		// Say what Discord listed, without any names, so the log explains a refusal.
		return Result{}, fmt.Errorf("%w (Discord listed: %s)", ErrNoBattlenet, Describe(conns))
	}
	return Result{UserID: me.ID, Tag: tag}, nil
}

// Describe summarises a connection list for the log: types and verified flags only,
// never the account names.
func Describe(conns []Connection) string {
	if len(conns) == 0 {
		return "no connections"
	}
	parts := make([]string, 0, len(conns))
	for _, c := range conns {
		parts = append(parts, fmt.Sprintf("%s(verified=%t, name=%d chars)", c.Type, c.Verified, len(c.Name)))
	}
	return strings.Join(parts, ", ")
}

// ErrNoBattlenet means the Discord account has no verified Battle.net connection.
var ErrNoBattlenet = errors.New("no verified Battle.net connection on this Discord account")

// BattlenetTag picks the verified Battle.net connection, if any. Discord stores the
// battletag as the connection's name. Battle.net links are made through Blizzard's own
// login, so any name Discord reports for one is taken as is, with or without the
// #digits part.
func BattlenetTag(conns []Connection) (string, bool) {
	for _, c := range conns {
		if c.Type == "battlenet" && c.Verified && strings.TrimSpace(c.Name) != "" {
			return strings.TrimSpace(c.Name), true
		}
	}
	return "", false
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("discord answered %d", resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}
