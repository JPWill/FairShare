-- Stand-ins for the WoW API used by the addon. Deliberately permissive: any method the
-- addon calls on a frame that is not modelled here is a no-op, so UI code loads and runs
-- without the test suite having to model rendering. Protocol-relevant pieces (addon
-- messages, timers, events, time, roster) are modelled properly so tests can drive them.
--
-- Globals tests may set before/after loading:
--   PlayerName   name UnitName("player") returns          (default "Harnessplayer")
--   InRaid       IsInRaid()                                (default true)
--   InGroup      IsInGroup()                               (default true)
--   IsLeader     UnitIsGroupLeader("player")               (default false)
--   Roster       { {name, rank}, ... } for GetRaidRosterInfo (default: player + "Other", leader per IsLeader)
--   SendResult   what C_ChatInfo.SendAddonMessage returns  (default 0 = success)
-- Globals tests read:
--   SentMessages   successfully sent messages { prefix, msg, channel, target }
--   SendAttempts   count of every SendAddonMessage call, successful or not
-- Helpers:
--   FireEvent(event, ...)   deliver a game event to every registered frame
--   AdvanceTime(seconds)    move the clock and run due timers in order

ServerTime = 1000000
PlayerName = PlayerName or "Harnessplayer"
InRaid = (InRaid == nil) and true or InRaid
InGroup = (InGroup == nil) and true or InGroup
IsLeader = IsLeader or false
SendResult = SendResult or 0
Roster = Roster or { { PlayerName, IsLeader and 2 or 0 }, { "Other", IsLeader and 0 or 2 } }

function GetServerTime() return ServerTime end
function GetTime() return ServerTime end
function debugprofilestop() return 0 end
function GetCursorPosition() return 0, 0 end
function PlaySound() end
function print() end
function date(fmt) return "00:00:00" end
function UnitName(unit) return PlayerName end
function UnitFullName(unit)
    if unit == "player" then return PlayerName, "Harness" end
    local i = tonumber(unit:match("^party(%d+)$") or "")
    if i and Roster[i + 1] then return Roster[i + 1][1], "Harness" end
    return nil
end
function GetRealmName() return "Harness" end
function GetNormalizedRealmName() return "Harness" end
function IsInRaid() return InRaid end
function IsInGroup() return InGroup end
function UnitIsGroupLeader(unit)
    if unit == "player" then return IsLeader end
    local i = tonumber(unit:match("^party(%d+)$") or "")
    return i and Roster[i + 1] and Roster[i + 1][2] == 2 or false
end
function GetNumGroupMembers() return InGroup and #Roster or 0 end
function GetRaidRosterInfo(i)
    local r = Roster[i]
    if not r then return nil end
    return r[1], r[2]
end

math.atan2 = math.atan2 or function(y, x)
    if x > 0 then return math.atan(y / x) end
    if x < 0 then return math.atan(y / x) + (y >= 0 and math.pi or -math.pi) end
    return y > 0 and math.pi / 2 or -math.pi / 2
end

Enum = { SendAddonMessageResult = { Success = 0, AddonMessageThrottle = 3, InvalidChatType = 4, AddOnMessageLockdown = 11 } }
SOUNDKIT = { RAID_WARNING = 8959 }
LOOT_ITEM = "%s receives loot: %s."
LOOT_ITEM_SELF = "You receive loot: %s."
LOOT_ITEM_MULTIPLE = "%s receives loot: %sx%d."
LOOT_ITEM_SELF_MULTIPLE = "You receive loot: %sx%d."

-- Item info: equip location comes from a test-settable table keyed by item id.
ItemEquipLocs = ItemEquipLocs or { ["6948"] = "", ["19019"] = "INVTYPE_WEAPON", ["17076"] = "INVTYPE_2HWEAPON" }
local function itemInfo(link)
    local id = tostring(link):match("item:(%d+)")
    local name = tostring(link):match("%[(.-)%]") or "Item"
    return name, link, 4, 60, 60, "Weapon", "Sword", 1, ItemEquipLocs[id] or "INVTYPE_HEAD", "", 0
end
GetItemInfo = itemInfo
C_Item = { GetItemInfo = itemInfo }

----------------------------------------------------------------------------- messaging

SentMessages = {}
SendAttempts = 0
C_ChatInfo = {
    RegisterAddonMessagePrefix = function(prefix) return true end,
    IsAddonMessagePrefixRegistered = function(prefix) return true end,
    SendAddonMessage = function(prefix, msg, channel, target)
        SendAttempts = SendAttempts + 1
        if SendResult == 0 then
            SentMessages[#SentMessages + 1] = { prefix = prefix, msg = msg, channel = channel, target = target }
        end
        return SendResult
    end,
}

C_AddOns = { GetAddOnMetadata = function(name, field) return field == "Version" and "test" or nil end }

-- Invites are recorded; tests decide when the group "forms" by changing InGroup/InRaid/Roster.
Invites = {}
Converted = 0
C_PartyInfo = {
    InviteUnit = function(name) Invites[#Invites + 1] = name end,
    ConvertToRaid = function() Converted = Converted + 1; InRaid = true end,
}

----------------------------------------------------------------------------- timers

Timers = {}
local function newTimer(delay, fn, every, iterations)
    local t = { at = ServerTime + delay, fn = fn, every = every, left = iterations, cancelled = false }
    function t:Cancel() self.cancelled = true end
    function t:IsCancelled() return self.cancelled end
    Timers[#Timers + 1] = t
    return t
end
C_Timer = {
    After = function(delay, fn) newTimer(delay, fn) end,
    NewTimer = function(delay, fn) return newTimer(delay, fn) end,
    NewTicker = function(delay, fn, iterations) return newTimer(delay, fn, delay, iterations) end,
}

function AdvanceTime(seconds)
    local target = ServerTime + seconds
    while true do
        local best, bi
        for i, t in ipairs(Timers) do
            if not t.cancelled and (not best or t.at < best.at) then best, bi = t, i end
        end
        if not best or best.at > target then break end
        ServerTime = best.at
        if best.every then
            best.at = best.at + best.every
            if best.left then
                best.left = best.left - 1
                if best.left <= 0 then best.cancelled = true end
            end
            best.fn(best)
        else
            table.remove(Timers, bi)
            best.fn()
        end
    end
    for i = #Timers, 1, -1 do
        if Timers[i].cancelled then table.remove(Timers, i) end
    end
    ServerTime = target
end

----------------------------------------------------------------------------- frames

local noop = function() end
local methods = {}
local Object = { __index = function(t, k) return methods[k] or noop end }

local function newObject(kind, name, parent)
    return setmetatable({
        kind = kind, name = name, parent = parent,
        events = {}, scripts = {}, shown = true, width = 0, height = 0, text = "", checked = false, focus = false,
    }, Object)
end

function methods:RegisterEvent(e) self.events[e] = true end
function methods:UnregisterEvent(e) self.events[e] = nil end
function methods:UnregisterAllEvents() self.events = {} end
function methods:SetScript(k, fn) self.scripts[k] = fn end
function methods:GetScript(k) return self.scripts[k] end
function methods:Show() self.shown = true end
function methods:Hide() self.shown = false end
function methods:IsShown() return self.shown end
function methods:SetSize(w, h) self.width, self.height = w, h end
function methods:SetWidth(w) self.width = w end
function methods:SetHeight(h) self.height = h end
function methods:GetWidth() return self.width end
function methods:GetHeight() return self.height end
function methods:SetText(s) self.text = tostring(s) end
function methods:GetText() return self.text end
function methods:SetPoint(point, ...) local p = rawget(self, "points") or {}; p[#p + 1] = point; rawset(self, "points", p) end
function methods:ClearAllPoints() rawset(self, "points", {}) end
function methods:GetNumPoints() local p = rawget(self, "points"); return p and #p or 0 end
function methods:GetPoint(i) local p = rawget(self, "points"); return p and p[i or 1] or nil end
function methods:IsVisible() return self.shown end
function methods:GetName() return self.name end
function methods:GetParent() return self.parent end
function methods:GetCenter() return 0, 0 end
function methods:GetEffectiveScale() return 1 end
function methods:GetFont() return nil end
function methods:GetChecked() return self.checked end
function methods:SetChecked(v) self.checked = v and true or false end
function methods:SetFocus() self.focus = true end
function methods:ClearFocus() self.focus = false end
function methods:HasFocus() return self.focus end
function methods:CreateFontString(name, layer, template) return newObject("FontString", name, self) end
function methods:CreateTexture(name, layer) return newObject("Texture", name, self) end

Frames = {}
function CreateFrame(kind, name, parent, template)
    local f = newObject(kind, name, parent)
    f.template = template
    if name then _G[name] = f end
    Frames[#Frames + 1] = f
    return f
end

function FireEvent(event, ...)
    for _, f in ipairs(Frames) do
        if f.events[event] and f.scripts.OnEvent then f.scripts.OnEvent(f, event, ...) end
    end
end

UIParent = newObject("Frame", "UIParent")
Minimap = newObject("Frame", "Minimap")
GameTooltip = newObject("GameTooltip", "GameTooltip")
UISpecialFrames = {}
function hooksecurefunc() end
function GetCursorInfo() return nil end
function ClearCursor() end
function ChatEdit_GetActiveWindow() return nil end
function IsInInstance() return false end
function InitiateTrade() end
function ClickTradeButton() end
NUM_BAG_SLOTS = 4
C_Container = { GetContainerNumSlots = function() return 0 end, GetContainerItemLink = function() return nil end, PickupContainerItem = function() end }
