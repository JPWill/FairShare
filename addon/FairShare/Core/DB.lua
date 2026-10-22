-- FairShare/Core/DB.lua
-- SavedVariables: fills in defaults on load and exposes the table as ns.db.
local ADDON, ns = ...

local DB = {}
ns.Core.DB = DB

local defaults = {
    settings = {
        theme = "auto",                     -- auto | default | elvui
        minimap = { hide = false, angle = 220 },
        sound = true,
        addonKey = "",                      -- 32 hex chars from the bot; signs COMMIT/REVEAL/ACK
    },
    transcript = {},                        -- raidID -> ordered list of {t, dir, sender, msg}
}

local function applyDefaults(dst, src)
    for k, v in pairs(src) do
        if type(v) == "table" then
            if type(dst[k]) ~= "table" then dst[k] = {} end
            applyDefaults(dst[k], v)
        elseif dst[k] == nil then
            dst[k] = v
        end
    end
end

ns.Core.Events.Register("ADDON_LOADED", function(_, name)
    if name ~= ADDON then return end
    if type(_G.FairShareDB) ~= "table" then _G.FairShareDB = {} end
    applyDefaults(_G.FairShareDB, defaults)
    ns.db = _G.FairShareDB
    ns.Core.Events.Fire("DB_READY")
end)
