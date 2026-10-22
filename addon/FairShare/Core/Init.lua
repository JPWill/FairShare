-- FairShare: addon namespace and shared table.
-- Every file in this addon receives (addonName, ns) as varargs and attaches to ns.
local ADDON, ns = ...

ns.NAME = ADDON
ns.VERSION = C_AddOns and C_AddOns.GetAddOnMetadata and C_AddOns.GetAddOnMetadata(ADDON, "Version") or "dev"
ns.PREFIX = "FAIRSHARE" -- addon message prefix, <= 16 chars
ns.SITE_URL = "https://fairsharewow.com" -- community website: ledger, FAQ, rules. Addons cannot open links, so it is shown to copy.

-- Sub-tables are created here so load order between feature files never matters.
ns.Lib = ns.Lib or {}
ns.Core = {}
ns.Snapshot = {}
ns.Auction = {}
ns.Transcript = {}
ns.UI = {}

function ns.Print(...)
    print("|cff33ff99FairShare|r:", ...)
end

_G.FairShare = ns -- exposed for /dump and the test harness; not an API
