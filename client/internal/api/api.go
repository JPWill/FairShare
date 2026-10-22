// Package api is the sync client's view of the backend HTTP API (docs/protocol.md §7).
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to one backend with one bearer token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a client; baseURL may or may not end with a slash.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ErrNoRaid is returned by CurrentSnapshot when the caller leads no unsettled raid.
var ErrNoRaid = errors.New("no open raid for this account")

// ErrUnauthorized means the token was rejected.
var ErrUnauthorized = errors.New("token rejected by the backend (generate a new one from the bot's personal panel)")

// Snapshot is the response of GET /snapshot/current.
type Snapshot struct {
	RaidID   string `json:"raidId"`
	Text     string `json:"text"`
	Checksum string `json:"checksum"`
	IssuedAt int64  `json:"issuedAt"`
}

// UploadResult is the response of POST /transcripts.
type UploadResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Me is the response of GET /me.
type Me struct {
	DiscordID   string `json:"discordId"`
	DisplayName string `json:"displayName"`
}

func (c *Client) do(method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.BaseURL+"/api/v1"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("backend unreachable at %s: %w", c.BaseURL, err)
		}
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return ErrNoRaid
	case resp.StatusCode >= 400:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("backend returned %d: %s", resp.StatusCode, e.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("backend sent an unexpected reply: %w", err)
	}
	return nil
}

// Me verifies the token and returns who it belongs to.
func (c *Client) Me() (*Me, error) {
	var m Me
	if err := c.do(http.MethodGet, "/me", nil, &m); err != nil {
		if errors.Is(err, ErrNoRaid) {
			return nil, fmt.Errorf("backend has no /me endpoint at %s (wrong URL?)", c.BaseURL)
		}
		return nil, err
	}
	return &m, nil
}

// CurrentSnapshot fetches the newest unsettled raid led by the token's owner.
func (c *Client) CurrentSnapshot() (*Snapshot, error) {
	var s Snapshot
	if err := c.do(http.MethodGet, "/snapshot/current", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// UploadTranscript posts one raid's canonical transcript text as a witness statement:
// the backend may settle the raid on it.
func (c *Client) UploadTranscript(raidID, text string) (*UploadResult, error) {
	return c.upload(raidID, text, false)
}

// UploadProgress posts a mid-raid transcript as a live update: the backend refreshes
// the live ledger in Discord and settles nothing. Only the raid's leader may send one.
func (c *Client) UploadProgress(raidID, text string) (*UploadResult, error) {
	return c.upload(raidID, text, true)
}

func (c *Client) upload(raidID, text string, live bool) (*UploadResult, error) {
	var r UploadResult
	body := map[string]interface{}{"raidId": raidID, "text": text}
	if live {
		body["live"] = true
	}
	if err := c.do(http.MethodPost, "/transcripts", body, &r); err != nil {
		if errors.Is(err, ErrNoRaid) {
			return nil, fmt.Errorf("backend does not know raid %s", raidID)
		}
		return nil, err
	}
	return &r, nil
}

// Finish asks the backend to settle a raid from its last live update (leader only).
func (c *Client) Finish(raidID string) (*UploadResult, error) {
	var r UploadResult
	if err := c.do(http.MethodPost, "/raids/"+raidID+"/finish", nil, &r); err != nil {
		if errors.Is(err, ErrNoRaid) {
			return nil, fmt.Errorf("backend does not know raid %s", raidID)
		}
		return nil, err
	}
	return &r, nil
}
