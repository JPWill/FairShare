// Package addontest loads the FairShare addon into a Lua 5.1 interpreter
// (gopher-lua) with stubs for the WoW API so protocol logic can be tested
// outside the game. Developer tool only; nothing here ships.
package addontest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// AddonDir is the addon folder relative to this package.
const AddonDir = "../../addon/FairShare"

// Harness is one loaded instance of the addon: one player, one Lua state.
type Harness struct {
	L         *lua.LState
	NS        *lua.LTable // the addon's private namespace table (second vararg to every file)
	Delivered int         // how many of SentMessages() an echo pump has already delivered back
}

// Sent is one successfully sent addon message captured by the stubs.
type Sent struct {
	Prefix, Msg, Channel, Target string
}

// Options for New.
type Options struct {
	PlayerName string // UnitName("player"); default "Harnessplayer"
	Leader     bool   // UnitIsGroupLeader("player")
}

// New builds a fresh Lua state, installs the bit library and the WoW stubs, and loads
// every Lua file listed in the TOC, in order, passing (addonName, ns) as varargs exactly
// as the game does. Missing optional files (Data.lua) are skipped.
func New(opts ...Options) (*Harness, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	L := lua.NewState()
	ns := L.NewTable()
	h := &Harness{L: L, NS: ns}

	installBit(L)
	if o.PlayerName != "" {
		L.SetGlobal("PlayerName", lua.LString(o.PlayerName))
	}
	L.SetGlobal("IsLeader", lua.LBool(o.Leader))

	if err := L.DoFile(filepath.Join("stubs", "wow.lua")); err != nil {
		L.Close()
		return nil, fmt.Errorf("stubs: %w", err)
	}

	files, err := tocFiles(filepath.Join(AddonDir, "FairShare.toc"))
	if err != nil {
		L.Close()
		return nil, err
	}
	for _, rel := range files {
		path := filepath.Join(AddonDir, rel)
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			if rel == "Data.lua" {
				continue
			}
			L.Close()
			return nil, fmt.Errorf("TOC lists %s but it does not exist", rel)
		}
		if err := h.loadFile(path, rel); err != nil {
			L.Close()
			return nil, err
		}
	}
	return h, nil
}

// Close releases the Lua state.
func (h *Harness) Close() { h.L.Close() }

// Login fires ADDON_LOADED for the addon and then PLAYER_LOGIN, as the game does.
func (h *Harness) Login() error {
	if err := h.FireEvent("ADDON_LOADED", lua.LString("FairShare")); err != nil {
		return err
	}
	return h.FireEvent("PLAYER_LOGIN")
}

// FireEvent delivers a game event to every frame registered for it.
func (h *Harness) FireEvent(event string, args ...lua.LValue) error {
	fn := h.L.GetGlobal("FireEvent")
	all := append([]lua.LValue{lua.LString(event)}, args...)
	return h.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, all...)
}

// AdvanceTime moves the stub clock forward and runs every timer that comes due, in order.
func (h *Harness) AdvanceTime(seconds float64) error {
	fn := h.L.GetGlobal("AdvanceTime")
	return h.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, lua.LNumber(seconds))
}

// Do runs a chunk of Lua.
func (h *Harness) Do(code string) error { return h.L.DoString(code) }

// Eval evaluates a Lua expression and returns its first result (extra results are dropped).
func (h *Harness) Eval(expr string) (lua.LValue, error) {
	if err := h.L.DoString("return (" + expr + ")"); err != nil {
		return lua.LNil, err
	}
	v := h.L.Get(-1)
	h.L.Pop(1)
	return v, nil
}

// Set assigns a Lua global.
func (h *Harness) Set(name string, v lua.LValue) { h.L.SetGlobal(name, v) }

// SentMessages returns the addon messages the stubs recorded as successfully sent.
func (h *Harness) SentMessages() []Sent {
	tbl, ok := h.L.GetGlobal("SentMessages").(*lua.LTable)
	if !ok {
		return nil
	}
	var out []Sent
	tbl.ForEach(func(_, v lua.LValue) {
		row, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		out = append(out, Sent{
			Prefix:  row.RawGetString("prefix").String(),
			Msg:     row.RawGetString("msg").String(),
			Channel: row.RawGetString("channel").String(),
			Target:  row.RawGetString("target").String(),
		})
	})
	return out
}

// ClearSent empties the recorded sent list and resets the echo pump's position.
func (h *Harness) ClearSent() {
	h.L.SetGlobal("SentMessages", h.L.NewTable())
	h.Delivered = 0
}

// Deliver feeds an addon message into this harness as if sender had sent it on channel.
func (h *Harness) Deliver(msg Sent, sender string) error {
	return h.FireEvent("CHAT_MSG_ADDON", lua.LString(msg.Prefix), lua.LString(msg.Msg), lua.LString(msg.Channel), lua.LString(sender))
}

func (h *Harness) loadFile(path, rel string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fn, err := h.L.Load(strings.NewReader(string(src)), rel)
	if err != nil {
		return fmt.Errorf("compile %s: %w", rel, err)
	}
	h.L.Push(fn)
	h.L.Push(lua.LString("FairShare"))
	h.L.Push(h.NS)
	if err := h.L.PCall(2, 0, nil); err != nil {
		return fmt.Errorf("run %s: %w", rel, err)
	}
	return nil
}

// tocFiles returns the .lua entries of a TOC file in order. XML and metadata lines are ignored.
func tocFiles(tocPath string) ([]string, error) {
	f, err := os.Open(tocPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(line), ".lua") {
			out = append(out, filepath.FromSlash(line))
		}
	}
	return out, sc.Err()
}

// installBit provides the game's `bit` library (LuaBitOp semantics: operands wrap to
// 32 bits, results come back as signed 32-bit numbers). The addon's Lib/Bit.lua
// normalizes the sign, which this deliberately exercises.
func installBit(L *lua.LState) {
	toU32 := func(v lua.LValue) uint32 { return uint32(int64(float64(lua.LVAsNumber(v)))) }
	ret := func(L *lua.LState, u uint32) int { L.Push(lua.LNumber(float64(int32(u)))); return 1 }
	fold := func(op func(a, b uint32) uint32) lua.LGFunction {
		return func(L *lua.LState) int {
			acc := toU32(L.CheckNumber(1))
			for i := 2; i <= L.GetTop(); i++ {
				acc = op(acc, toU32(L.CheckNumber(i)))
			}
			return ret(L, acc)
		}
	}
	bit := L.NewTable()
	L.SetFuncs(bit, map[string]lua.LGFunction{
		"band":   fold(func(a, b uint32) uint32 { return a & b }),
		"bor":    fold(func(a, b uint32) uint32 { return a | b }),
		"bxor":   fold(func(a, b uint32) uint32 { return a ^ b }),
		"bnot":   func(L *lua.LState) int { return ret(L, ^toU32(L.CheckNumber(1))) },
		"tobit":  func(L *lua.LState) int { return ret(L, toU32(L.CheckNumber(1))) },
		"lshift": func(L *lua.LState) int { return ret(L, toU32(L.CheckNumber(1))<<(toU32(L.CheckNumber(2))&31)) },
		"rshift": func(L *lua.LState) int { return ret(L, toU32(L.CheckNumber(1))>>(toU32(L.CheckNumber(2))&31)) },
		"arshift": func(L *lua.LState) int {
			return ret(L, uint32(int32(toU32(L.CheckNumber(1)))>>(toU32(L.CheckNumber(2))&31)))
		},
	})
	L.SetGlobal("bit", bit)
}
