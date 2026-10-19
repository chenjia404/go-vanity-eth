//go:build windows && amd64

package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func cudaForTest(t *testing.T) *cudaSearcher {
	t.Helper()
	if os.Getenv("CUDA_TEST") != "1" {
		t.Skip("设置 CUDA_TEST=1 后执行显卡实机测试")
	}
	engine, err := newCUDASearcher(config{Continuous: 2}, options{lanes: 128, steps: 4, ptx: "cuda/vanity.ptx"})
	if err != nil {
		t.Fatal(err)
	}
	s := engine.(*cudaSearcher)
	t.Cleanup(s.close)
	return s
}

func decodeField(b []byte) *big.Int {
	be := make([]byte, 32)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(be[28-i*4:], binary.LittleEndian.Uint32(b[i*4:]))
	}
	return new(big.Int).SetBytes(be)
}

func TestCUDAFieldAgainstBigInt(t *testing.T) {
	s := cudaForTest(t)
	p := crypto.S256().Params().P
	edges := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), new(big.Int).Sub(p, big.NewInt(1)), new(big.Int).Sub(p, big.NewInt(2)), new(big.Int).Rsh(new(big.Int).Set(p), 1)}
	const n = 1024
	a, b := make([]*big.Int, n), make([]*big.Int, n)
	inputA, inputB := make([]byte, n*32), make([]byte, n*32)
	for i := 0; i < n; i++ {
		var err error
		if i < len(edges)*len(edges) {
			a[i] = edges[i/len(edges)]
			b[i] = edges[i%len(edges)]
		} else {
			a[i], err = rand.Int(rand.Reader, p)
			if err != nil {
				t.Fatal(err)
			}
			b[i], err = rand.Int(rand.Reader, p)
			if err != nil {
				t.Fatal(err)
			}
		}
		encodeField(inputA[i*32:], a[i])
		encodeField(inputB[i*32:], b[i])
	}
	pa, err := s.d.alloc(n * 32)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := s.d.alloc(n * 32)
	if err != nil {
		t.Fatal(err)
	}
	po, err := s.d.alloc(n * 96)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.d.put(pa, inputA); err != nil {
		t.Fatal(err)
	}
	if err = s.d.put(pb, inputB); err != nil {
		t.Fatal(err)
	}
	f, err := s.d.function("fieldtest")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.d.launch(f, n, pa, pb, po, n); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, n*96)
	if err = s.d.get(po, out); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		wants := []*big.Int{new(big.Int).Add(a[i], b[i]), new(big.Int).Sub(a[i], b[i]), new(big.Int).Mul(a[i], b[i])}
		for j, want := range wants {
			want.Mod(want, p)
			got := decodeField(out[(i*3+j)*32:])
			if got.Cmp(want) != 0 {
				t.Fatalf("素域运算不一致：向量 %d，运算 %d", i, j)
			}
		}
	}
}

func TestCUDAAddressesAndMatching(t *testing.T) {
	s := cudaForTest(t)
	keys := make([]*ecdsa.PrivateKey, 128)
	points := make([]byte, 128*64)
	for i := range keys {
		var err error
		keys[i], err = crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			keys[i], err = crypto.ToECDSA(big.NewInt(1).FillBytes(make([]byte, 32)))
			if err != nil {
				t.Fatal(err)
			}
		}
		encodePoint(points[i*64:], &keys[i].PublicKey)
	}
	if err := s.d.put(s.points, points); err != nil {
		t.Fatal(err)
	}
	// 两批独立核函数调用覆盖点加倍、随机大标量与显存状态延续。
	for batch := 0; batch < 2; batch++ {
		hits, err := s.batch(128, 4, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 512 {
			t.Fatal("地址数量不一致")
		}
		seen := map[uint64]bool{}
		for _, h := range hits {
			id := uint64(h.lane)*4 + uint64(h.iteration)
			if seen[id] {
				t.Fatal("返回重复位置")
			}
			seen[id] = true
			k := new(big.Int).Add(keys[h.lane].D, big.NewInt(int64(batch*4)+int64(h.iteration)))
			key, e := crypto.ToECDSA(k.FillBytes(make([]byte, 32)))
			if e != nil {
				t.Fatal(e)
			}
			if crypto.PubkeyToAddress(key.PublicKey) != h.address {
				t.Fatalf("地址不一致：批次 %d，线程 %d，偏移 %d", batch, h.lane, h.iteration)
			}
		}
	}
	full := crypto.PubkeyToAddress(keys[7].PublicKey).Hex()[2:]
	rules := []config{
		{Continuous: 1}, {Continuous: 2}, {Continuous: 3}, {Continuous: 40},
		{DreamAddressSubstr: []string{full}},
		{DreamAddressSubstr: []string{full[:4], full[30:]}},
		{Continuous: 3, DreamAddressSubstr: []string{"ab", "AB", "12"}},
	}
	for _, c := range rules {
		if err := s.setRules(c); err != nil {
			t.Fatal(err)
		}
		if err := s.d.put(s.points, points); err != nil {
			t.Fatal(err)
		}
		hits, err := s.batch(128, 4, false)
		if err != nil {
			t.Fatal(err)
		}
		got := map[uint64]bool{}
		for _, h := range hits {
			id := uint64(h.lane)*4 + uint64(h.iteration)
			if got[id] {
				t.Fatal("匹配返回重复位置")
			}
			got[id] = true
		}
		for lane, key := range keys {
			for step := 0; step < 4; step++ {
				k := new(big.Int).Add(key.D, big.NewInt(int64(step)))
				candidate, e := crypto.ToECDSA(k.FillBytes(make([]byte, 32)))
				if e != nil {
					t.Fatal(e)
				}
				want := c.matches(crypto.PubkeyToAddress(candidate.PublicKey))
				if got[uint64(lane*4+step)] != want {
					t.Fatalf("CPU/GPU 匹配不一致：规则 %+v，线程 %d，偏移 %d", c, lane, step)
				}
			}
		}
	}
}

func TestCUDARejectsInfinity(t *testing.T) {
	s := cudaForTest(t)
	points := make([]byte, 128*64)
	for i := 0; i < 128; i++ {
		k := big.NewInt(int64(i + 1))
		if i == 0 {
			k.Sub(crypto.S256().Params().N, big.NewInt(1))
		}
		key, err := crypto.ToECDSA(k.FillBytes(make([]byte, 32)))
		if err != nil {
			t.Fatal(err)
		}
		encodePoint(points[i*64:], &key.PublicKey)
	}
	if err := s.d.put(s.points, points); err != nil {
		t.Fatal(err)
	}
	if _, err := s.batch(128, 1, true); err == nil {
		t.Fatal("未拒绝无穷远点")
	}
}
