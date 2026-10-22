-- FairShare/Lib/Bit.lua
-- 32-bit unsigned bitwise helpers. Uses the game's bit library when present and a
-- pure-Lua fallback otherwise (the offline test suite provides its own bit library).
-- Every function takes and returns numbers in [0, 2^32). Inputs may be any value in
-- that range; outputs are always normalized, whatever sign convention the host uses.
local ADDON, ns = ...
ns.Lib = ns.Lib or {}

local MOD = 4294967296 -- 2^32
local Bit = {}
ns.Lib.Bit = Bit

local native = _G.bit
if native and native.band and native.bor and native.bxor and native.bnot and native.lshift and native.rshift then
    local band, bor, bxor, bnot, lshift, rshift = native.band, native.bor, native.bxor, native.bnot, native.lshift, native.rshift
    function Bit.band(a, b) return band(a, b) % MOD end
    function Bit.bor(a, b) return bor(a, b) % MOD end
    function Bit.bxor(a, b) return bxor(a, b) % MOD end
    function Bit.bnot(a) return bnot(a) % MOD end
    function Bit.lshift(a, n) if n >= 32 then return 0 end return lshift(a, n) % MOD end
    function Bit.rshift(a, n) if n >= 32 then return 0 end return rshift(a, n) % MOD end
    Bit.native = true
else
    -- Nibble lookup tables: 256 entries each, built once.
    local AND, OR, XOR = {}, {}, {}
    for i = 0, 15 do
        for j = 0, 15 do
            local a, o, x, p = 0, 0, 0, 1
            local ii, jj = i, j
            for _ = 1, 4 do
                local bi, bj = ii % 2, jj % 2
                if bi == 1 and bj == 1 then a = a + p end
                if bi == 1 or bj == 1 then o = o + p end
                if bi ~= bj then x = x + p end
                ii, jj = (ii - bi) / 2, (jj - bj) / 2
                p = p * 2
            end
            AND[i * 16 + j], OR[i * 16 + j], XOR[i * 16 + j] = a, o, x
        end
    end
    local function op(T, a, b)
        a, b = a % MOD, b % MOD
        local r, p = 0, 1
        for _ = 1, 8 do
            local na, nb = a % 16, b % 16
            r = r + T[na * 16 + nb] * p
            a, b = (a - na) / 16, (b - nb) / 16
            p = p * 16
        end
        return r
    end
    function Bit.band(a, b) return op(AND, a, b) end
    function Bit.bor(a, b) return op(OR, a, b) end
    function Bit.bxor(a, b) return op(XOR, a, b) end
    function Bit.bnot(a) return (MOD - 1) - (a % MOD) end
    function Bit.lshift(a, n) if n >= 32 then return 0 end return ((a % MOD) * 2 ^ n) % MOD end
    function Bit.rshift(a, n) if n >= 32 then return 0 end return math.floor((a % MOD) / 2 ^ n) end
    Bit.native = false
end

-- Rotate right within 32 bits. 0 < n < 32.
function Bit.rrot(x, n)
    return (Bit.rshift(x, n) + Bit.lshift(x, 32 - n)) % MOD
end

Bit.MOD = MOD
