package main

// GitHub Actions secret encryption: libsodium crypto_box_seal (X25519,
// BLAKE2b-192, XSalsa20-Poly1305). This implementation is cross-platform,
// standard-library-only, and interoperable with libsodium.
import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math/big"
)

var blakeIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}
var blakeSigma = [12][16]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
}

func rotr64(x uint64, n uint) uint64 { return (x >> n) | (x << (64 - n)) }
func blake2b192(m []byte) [24]byte {
	h := blakeIV
	h[0] ^= 0x01010018 // digest_size=24, fanout=1, depth=1
	var counter uint64
	for {
		n := len(m)
		if n > 128 {
			n = 128
		}
		block := make([]byte, 128)
		copy(block, m[:n])
		m = m[n:]
		counter += uint64(n)
		var words [16]uint64
		for i := 0; i < 16; i++ {
			words[i] = binary.LittleEndian.Uint64(block[i*8:])
		}
		var v [16]uint64
		copy(v[:8], h[:])
		copy(v[8:], blakeIV[:])
		v[12] ^= counter
		if len(m) == 0 {
			v[14] = ^v[14]
		}
		g := func(a, b, c, d int, x, y uint64) {
			v[a] += v[b] + x
			v[d] = rotr64(v[d]^v[a], 32)
			v[c] += v[d]
			v[b] = rotr64(v[b]^v[c], 24)
			v[a] += v[b] + y
			v[d] = rotr64(v[d]^v[a], 16)
			v[c] += v[d]
			v[b] = rotr64(v[b]^v[c], 63)
		}
		for r := 0; r < 12; r++ {
			s := blakeSigma[r]
			g(0, 4, 8, 12, words[s[0]], words[s[1]])
			g(1, 5, 9, 13, words[s[2]], words[s[3]])
			g(2, 6, 10, 14, words[s[4]], words[s[5]])
			g(3, 7, 11, 15, words[s[6]], words[s[7]])
			g(0, 5, 10, 15, words[s[8]], words[s[9]])
			g(1, 6, 11, 12, words[s[10]], words[s[11]])
			g(2, 7, 8, 13, words[s[12]], words[s[13]])
			g(3, 4, 9, 14, words[s[14]], words[s[15]])
		}
		for i := 0; i < 8; i++ {
			h[i] ^= v[i] ^ v[i+8]
		}
		if len(m) == 0 {
			break
		}
	}
	var ret [24]byte
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint64(ret[i*8:], h[i])
	}
	return ret
}
func rotl32(a uint32, n uint) uint32 { return a<<n | a>>(32-n) }

// salsaRounds performs Salsa20/20's 20 rounds (10 double rounds).
func salsaRounds(x *[16]uint32) {
	for i := 0; i < 10; i++ {
		x[4] ^= rotl32(x[0]+x[12], 7)
		x[8] ^= rotl32(x[4]+x[0], 9)
		x[12] ^= rotl32(x[8]+x[4], 13)
		x[0] ^= rotl32(x[12]+x[8], 18)
		x[9] ^= rotl32(x[5]+x[1], 7)
		x[13] ^= rotl32(x[9]+x[5], 9)
		x[1] ^= rotl32(x[13]+x[9], 13)
		x[5] ^= rotl32(x[1]+x[13], 18)
		x[14] ^= rotl32(x[10]+x[6], 7)
		x[2] ^= rotl32(x[14]+x[10], 9)
		x[6] ^= rotl32(x[2]+x[14], 13)
		x[10] ^= rotl32(x[6]+x[2], 18)
		x[3] ^= rotl32(x[15]+x[11], 7)
		x[7] ^= rotl32(x[3]+x[15], 9)
		x[11] ^= rotl32(x[7]+x[3], 13)
		x[15] ^= rotl32(x[11]+x[7], 18)
		x[1] ^= rotl32(x[0]+x[3], 7)
		x[2] ^= rotl32(x[1]+x[0], 9)
		x[3] ^= rotl32(x[2]+x[1], 13)
		x[0] ^= rotl32(x[3]+x[2], 18)
		x[6] ^= rotl32(x[5]+x[4], 7)
		x[7] ^= rotl32(x[6]+x[5], 9)
		x[4] ^= rotl32(x[7]+x[6], 13)
		x[5] ^= rotl32(x[4]+x[7], 18)
		x[11] ^= rotl32(x[10]+x[9], 7)
		x[8] ^= rotl32(x[11]+x[10], 9)
		x[9] ^= rotl32(x[8]+x[11], 13)
		x[10] ^= rotl32(x[9]+x[8], 18)
		x[12] ^= rotl32(x[15]+x[14], 7)
		x[13] ^= rotl32(x[12]+x[15], 9)
		x[14] ^= rotl32(x[13]+x[12], 13)
		x[15] ^= rotl32(x[14]+x[13], 18)
	}
}
func salsaState(key []byte, nonce8 []byte, counter uint64) [16]uint32 {
	var s [16]uint32
	s[0] = 0x61707865
	s[5] = 0x3320646e
	s[10] = 0x79622d32
	s[15] = 0x6b206574
	for i, j := range []int{1, 2, 3, 4, 11, 12, 13, 14} {
		s[j] = binary.LittleEndian.Uint32(key[i*4:])
	}
	s[6] = binary.LittleEndian.Uint32(nonce8)
	s[7] = binary.LittleEndian.Uint32(nonce8[4:])
	s[8] = uint32(counter)
	s[9] = uint32(counter >> 32)
	return s
}
func hsalsa20(key []byte, nonce16 []byte) [32]byte {
	s := salsaState(key, []byte{0, 0, 0, 0, 0, 0, 0, 0}, 0)
	s[6] = binary.LittleEndian.Uint32(nonce16)
	s[7] = binary.LittleEndian.Uint32(nonce16[4:])
	s[8] = binary.LittleEndian.Uint32(nonce16[8:])
	s[9] = binary.LittleEndian.Uint32(nonce16[12:])
	salsaRounds(&s)
	var out [32]byte
	for i, j := range []int{0, 5, 10, 15, 6, 7, 8, 9} {
		binary.LittleEndian.PutUint32(out[i*4:], s[j])
	}
	return out
}
func salsaStream(key []byte, nonce8 []byte, size int) []byte {
	out := make([]byte, size)
	for off, ctr := 0, uint64(0); off < size; off, ctr = off+64, ctr+1 {
		s := salsaState(key, nonce8, ctr)
		x := s
		salsaRounds(&x)
		for i := 0; i < 16; i++ {
			x[i] += s[i]
		}
		var block [64]byte
		for i := 0; i < 16; i++ {
			binary.LittleEndian.PutUint32(block[i*4:], x[i])
		}
		copy(out[off:], block[:])
	}
	return out
}
func leInt(b []byte) *big.Int {
	r := append([]byte(nil), b...)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return new(big.Int).SetBytes(r)
}
func poly1305(msg, key []byte) [16]byte {
	r := append([]byte(nil), key[:16]...)
	r[3] &= 15
	r[7] &= 15
	r[11] &= 15
	r[15] &= 15
	r[4] &= 252
	r[8] &= 252
	r[12] &= 252
	rv := leInt(r)
	sv := leInt(key[16:32])
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	acc := big.NewInt(0)
	for len(msg) > 0 {
		n := len(msg)
		if n > 16 {
			n = 16
		}
		v := leInt(msg[:n])
		v.Add(v, new(big.Int).Lsh(big.NewInt(1), uint(8*n)))
		acc.Add(acc, v)
		acc.Mul(acc, rv)
		acc.Mod(acc, p)
		msg = msg[n:]
	}
	acc.Add(acc, sv)
	acc.Mod(acc, new(big.Int).Lsh(big.NewInt(1), 128))
	raw := acc.Bytes()
	var out [16]byte
	for i := 0; i < len(raw); i++ {
		out[i] = raw[len(raw)-1-i]
	}
	return out
}
func secretBox(message []byte, nonce24 []byte, key []byte) []byte {
	sub := hsalsa20(key, nonce24[:16])
	stream := salsaStream(sub[:], nonce24[16:], len(message)+32)
	enc := make([]byte, len(message))
	for i := range message {
		enc[i] = message[i] ^ stream[i+32]
	}
	mac := poly1305(enc, stream[:32])
	return append(mac[:], enc...)
}
func sealForGitHub(message []byte, recipientPubKey []byte) ([]byte, error) {
	if len(recipientPubKey) != 32 {
		return nil, errors.New("GitHub secret key must have 32 bytes")
	}
	curve := ecdh.X25519()
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	recipient, err := curve.NewPublicKey(recipientPubKey)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	prekey := hsalsa20(shared, make([]byte, 16))
	concatenated := append(append([]byte{}, ephemeral.PublicKey().Bytes()...), recipientPubKey...)
	nonce := blake2b192(concatenated)
	sealed := secretBox(message, nonce[:], prekey[:])
	return append(ephemeral.PublicKey().Bytes(), sealed...), nil
}
