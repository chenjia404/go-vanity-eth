package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestRules(t *testing.T) {
	for _, c := range []config{{Continuous: -1}, {Continuous: 41}, {}, {DreamAddressSubstr: []string{""}}, {DreamAddressSubstr: []string{"0xabc"}}, {DreamAddressSubstr: []string{"xyz"}}} {
		if c.validate() == nil {
			t.Fatalf("无效配置未拒绝：%+v", c)
		}
	}
	addr := common.HexToAddress("0x52908400098527886E0F7030069857D2E4169EE7")
	for _, tc := range []struct {
		c    config
		want bool
	}{
		{config{Continuous: 1}, true},
		{config{Continuous: 2}, false},
		{config{DreamAddressSubstr: []string{"E0F703"}}, true},
		{config{DreamAddressSubstr: []string{"e0f703"}}, false},
		{config{Continuous: 40, DreamAddressSubstr: []string{"9852"}}, true},
	} {
		if err := tc.c.validate(); err != nil {
			t.Fatal(err)
		}
		if got := tc.c.matches(addr); got != tc.want {
			t.Fatalf("规则 %+v：得到 %t，期望 %t", tc.c, got, tc.want)
		}
	}
}

func TestCPUSearchStopsAndProducesValidKeys(t *testing.T) {
	var tried atomic.Uint64
	var found atomic.Uint64
	s := cpuSearcher{c: config{Continuous: 1}, workers: 4}
	err := s.run(context.Background(), func(key *ecdsa.PrivateKey, addr common.Address) error {
		if crypto.PubkeyToAddress(key.PublicKey) != addr {
			t.Error("私钥与地址不符")
		}
		found.Add(1)
		return errLimit
	}, &tried)
	if !errors.Is(err, errLimit) || found.Load() == 0 || tried.Load() < found.Load() {
		t.Fatalf("停止状态异常：%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.run(ctx, func(*ecdsa.PrivateKey, common.Address) error { return nil }, &tried); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消状态异常：%v", err)
	}
}
