-- FairShare/Lib/Sha256.lua
-- Pure Lua 5.1 SHA-256 (FIPS 180-4). Output must match Go's crypto/sha256 byte for byte;
-- the offline test suite checks that. Hex output avoids string.format("%x") on values
-- >= 2^31 because Lua 5.1's %x goes through a C long, which is 32-bit on Windows.
local ADDON, ns = ...
ns.Lib = ns.Lib or {}

local Bit = ns.Lib.Bit
local band, bxor, bnot, rshift, rrot = Bit.band, Bit.bxor, Bit.bnot, Bit.rshift, Bit.rrot
local MOD = Bit.MOD
local byte, char, rep, format, concat, floor = string.byte, string.char, string.rep, string.format, table.concat, math.floor

local Sha256 = {}
ns.Lib.Sha256 = Sha256

local K = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

local function tohex32(n)
    return format("%04x%04x", floor(n / 65536), n % 65536)
end

local function u32be(n)
    return char(floor(n / 16777216) % 256, floor(n / 65536) % 256, floor(n / 256) % 256, n % 256)
end

local function pad(msg)
    local len = #msg
    local extra = (64 - ((len + 9) % 64)) % 64
    local bits = len * 8
    return msg .. "\128" .. rep("\0", extra) .. u32be(floor(bits / MOD)) .. u32be(bits % MOD)
end

local w = {}
local function compress(H, data, off)
    for i = 0, 15 do
        local b1, b2, b3, b4 = byte(data, off + i * 4, off + i * 4 + 3)
        w[i] = ((b1 * 256 + b2) * 256 + b3) * 256 + b4
    end
    for i = 16, 63 do
        local w15, w2 = w[i - 15], w[i - 2]
        local s0 = bxor(bxor(rrot(w15, 7), rrot(w15, 18)), rshift(w15, 3))
        local s1 = bxor(bxor(rrot(w2, 17), rrot(w2, 19)), rshift(w2, 10))
        w[i] = (w[i - 16] + s0 + w[i - 7] + s1) % MOD
    end

    local a, b, c, d, e, f, g, h = H[1], H[2], H[3], H[4], H[5], H[6], H[7], H[8]
    for i = 0, 63 do
        local S1 = bxor(bxor(rrot(e, 6), rrot(e, 11)), rrot(e, 25))
        local ch = bxor(band(e, f), band(bnot(e), g))
        local t1 = (h + S1 + ch + K[i + 1] + w[i]) % MOD
        local S0 = bxor(bxor(rrot(a, 2), rrot(a, 13)), rrot(a, 22))
        local maj = bxor(bxor(band(a, b), band(a, c)), band(b, c))
        local t2 = (S0 + maj) % MOD
        h, g, f = g, f, e
        e = (d + t1) % MOD
        d, c, b = c, b, a
        a = (t1 + t2) % MOD
    end
    H[1] = (H[1] + a) % MOD; H[2] = (H[2] + b) % MOD; H[3] = (H[3] + c) % MOD; H[4] = (H[4] + d) % MOD
    H[5] = (H[5] + e) % MOD; H[6] = (H[6] + f) % MOD; H[7] = (H[7] + g) % MOD; H[8] = (H[8] + h) % MOD
end

-- Returns the lowercase 64-char hex digest of msg (a Lua string of raw bytes).
function Sha256.hex(msg)
    local H = { 0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19 }
    local data = pad(msg)
    for off = 1, #data, 64 do
        compress(H, data, off)
    end
    local out = {}
    for i = 1, 8 do out[i] = tohex32(H[i]) end
    return concat(out)
end

-- First n hex chars of the digest (the "short checksum" shown in the UI and in Discord).
function Sha256.short(msg, n)
    return Sha256.hex(msg):sub(1, n or 8)
end
