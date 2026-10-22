-- FairShare/Lib/Base64.lua
-- Standard base64 (RFC 4648, '+' '/' alphabet, '=' padding). Used for snapshot and
-- transcript strings so raw bytes never meet chat or edit-box escaping.
local ADDON, ns = ...
ns.Lib = ns.Lib or {}

local byte, char, concat = string.byte, string.char, table.concat
local Base64 = {}
ns.Lib.Base64 = Base64

local ALPHA = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
local ENC, DEC = {}, {}
for i = 1, 64 do
    local c = ALPHA:sub(i, i)
    ENC[i - 1] = c
    DEC[byte(c)] = i - 1
end

function Base64.encode(s)
    local out, n = {}, 0
    local len = #s
    for i = 1, len - 2, 3 do
        local a, b, c = byte(s, i, i + 2)
        local v = (a * 256 + b) * 256 + c
        n = n + 1
        out[n] = ENC[math.floor(v / 262144)] .. ENC[math.floor(v / 4096) % 64] .. ENC[math.floor(v / 64) % 64] .. ENC[v % 64]
    end
    local rem = len % 3
    if rem == 1 then
        local a = byte(s, len)
        local v = a * 65536
        n = n + 1
        out[n] = ENC[math.floor(v / 262144)] .. ENC[math.floor(v / 4096) % 64] .. "=="
    elseif rem == 2 then
        local a, b = byte(s, len - 1, len)
        local v = (a * 256 + b) * 256
        n = n + 1
        out[n] = ENC[math.floor(v / 262144)] .. ENC[math.floor(v / 4096) % 64] .. ENC[math.floor(v / 64) % 64] .. "="
    end
    return concat(out)
end

-- Returns the decoded string, or nil and an error message on malformed input.
-- Whitespace is ignored so strings pasted with line breaks still decode.
function Base64.decode(s)
    s = s:gsub("%s+", "")
    local len = #s
    if len % 4 ~= 0 then return nil, "length not a multiple of 4" end
    local out, n = {}, 0
    for i = 1, len, 4 do
        local c1, c2, c3, c4 = byte(s, i, i + 3)
        local v1, v2 = DEC[c1], DEC[c2]
        if not v1 or not v2 then return nil, "invalid character at " .. i end
        if c3 == 61 then -- '='
            if i + 3 ~= len or c4 ~= 61 then return nil, "bad padding at " .. i end
            n = n + 1
            out[n] = char(math.floor((v1 * 64 + v2) / 16) % 256)
        elseif c4 == 61 then
            if i + 3 ~= len then return nil, "bad padding at " .. i end
            local v3 = DEC[c3]
            if not v3 then return nil, "invalid character at " .. (i + 2) end
            local v = (v1 * 64 + v2) * 64 + v3
            n = n + 1
            out[n] = char(math.floor(v / 1024) % 256, math.floor(v / 4) % 256)
        else
            local v3, v4 = DEC[c3], DEC[c4]
            if not v3 or not v4 then return nil, "invalid character at " .. (i + 2) end
            local v = ((v1 * 64 + v2) * 64 + v3) * 64 + v4
            n = n + 1
            out[n] = char(math.floor(v / 65536), math.floor(v / 256) % 256, v % 256)
        end
    end
    return concat(out)
end
