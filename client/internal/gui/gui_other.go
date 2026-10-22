//go:build !windows

// Package gui is the sync client's window. It exists only on Windows; elsewhere the client
// runs in the console (fairshare-sync -cli).
package gui

import (
	"errors"

	"github.com/JPWill/FairShare/client/internal/app"
)

// Run reports that the window is Windows-only.
func Run(*app.Controller) error {
	return errors.New("the FairShare sync window is Windows-only; run fairshare-sync -cli")
}
