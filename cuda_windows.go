//go:build windows && amd64

package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/sys/windows"
)

// 使用驱动接口加载 PTX，无需为 Go 配置 CGO 或链接 CUDA 运行库。
type cudaDriver struct {
	dll         *windows.LazyDLL
	ctx, module uintptr
	allocations []uint64
}

func (d *cudaDriver) call(name string, args ...uintptr) error {
	p := d.dll.NewProc(name)
	if err := p.Find(); err != nil {
		return fmt.Errorf("CUDA 驱动缺少 %s：%w", name, err)
	}
	code, _, _ := p.Call(args...)
	if code != 0 {
		return fmt.Errorf("CUDA %s 失败，错误码 %d", name, code)
	}
	return nil
}

func openCUDA(device int, ptx string) (*cudaDriver, error) {
	d := &cudaDriver{dll: windows.NewLazySystemDLL("nvcuda.dll")}
	if err := d.dll.Load(); err != nil {
		return nil, fmt.Errorf("加载 NVIDIA CUDA 驱动失败：%w", err)
	}
	if err := d.call("cuInit", 0); err != nil {
		return nil, err
	}
	var dev int32
	if err := d.call("cuDeviceGet", uintptr(unsafe.Pointer(&dev)), uintptr(device)); err != nil {
		return nil, err
	}
	if err := d.call("cuCtxCreate_v2", uintptr(unsafe.Pointer(&d.ctx)), 0, uintptr(dev)); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(ptx)
	if err == nil {
		data = append(data, 0)
		err = d.call("cuModuleLoadData", uintptr(unsafe.Pointer(&d.module)), uintptr(unsafe.Pointer(unsafe.SliceData(data))))
		runtime.KeepAlive(data)
	}
	if err != nil {
		d.close()
		return nil, fmt.Errorf("加载内核失败，请先运行 scripts/build-cuda.ps1：%w", err)
	}
	var name [256]byte
	if err = d.call("cuDeviceGetName", uintptr(unsafe.Pointer(&name[0])), 256, uintptr(dev)); err != nil {
		d.close()
		return nil, err
	}
	fmt.Printf("CUDA 显卡：%s\n", windows.BytePtrToString(&name[0]))
	return d, nil
}

func (d *cudaDriver) close() {
	for i := len(d.allocations) - 1; i >= 0; i-- {
		_ = d.call("cuMemFree_v2", uintptr(d.allocations[i]))
	}
	if d.module != 0 {
		_ = d.call("cuModuleUnload", d.module)
	}
	if d.ctx != 0 {
		_ = d.call("cuCtxDestroy_v2", d.ctx)
	}
}

func (d *cudaDriver) alloc(size int) (uint64, error) {
	var p uint64
	err := d.call("cuMemAlloc_v2", uintptr(unsafe.Pointer(&p)), uintptr(size))
	if err == nil {
		d.allocations = append(d.allocations, p)
	}
	return p, err
}

func (d *cudaDriver) put(p uint64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	err := d.call("cuMemcpyHtoD_v2", uintptr(p), uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(len(data)))
	runtime.KeepAlive(data)
	return err
}

func (d *cudaDriver) get(p uint64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	err := d.call("cuMemcpyDtoH_v2", uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(p), uintptr(len(data)))
	runtime.KeepAlive(data)
	return err
}

func (d *cudaDriver) function(name string) (uintptr, error) {
	n, err := windows.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	var f uintptr
	err = d.call("cuModuleGetFunction", uintptr(unsafe.Pointer(&f)), d.module, uintptr(unsafe.Pointer(n)))
	runtime.KeepAlive(n)
	return f, err
}

func (d *cudaDriver) launch(f uintptr, lanes int, values ...uint64) error {
	params := make([]uintptr, len(values))
	for i := range values {
		params[i] = uintptr(unsafe.Pointer(&values[i]))
	}
	err := d.call("cuLaunchKernel", f, uintptr(lanes/128), 1, 1, 128, 1, 1, 0, 0, uintptr(unsafe.Pointer(unsafe.SliceData(params))), 0)
	runtime.KeepAlive(values)
	runtime.KeepAlive(params)
	if err != nil {
		return err
	}
	return d.call("cuCtxSynchronize")
}

type gpuHit struct {
	lane, iteration uint32
	address         common.Address
}

type cudaSearcher struct {
	d                                 *cudaDriver
	c                                 config
	lanes, steps                      int
	function                          uintptr
	points, rules, hits, count, fault uint64
	seeds                             []*big.Int
	offset                            uint64
}

func newCUDASearcher(c config, o options) (searcher, error) {
	if o.lanes < 128 || o.lanes > 65536 || o.lanes%128 != 0 || o.steps < 1 || o.steps > 64 {
		return nil, errors.New("CUDA lanes 必须是 128 到 65536 之间的 128 的倍数，steps 必须在 1 到 64 之间")
	}
	// CUDA 上下文绑定系统线程，初始化、运行、清理均由此协程完成。
	runtime.LockOSThread()
	d, err := openCUDA(o.device, o.ptx)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	s := &cudaSearcher{d: d, c: c, lanes: o.lanes, steps: o.steps}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	s.function, err = d.function("search")
	if err != nil {
		return nil, err
	}
	for _, a := range []struct {
		dst  *uint64
		size int
	}{
		{&s.points, o.lanes * 64}, {&s.rules, 8 + 32*4 + 32*40},
		{&s.hits, maxInt(o.lanes*o.steps, 128*4) * 28}, {&s.count, 4}, {&s.fault, 4},
	} {
		*a.dst, err = d.alloc(a.size)
		if err != nil {
			return nil, err
		}
	}
	if err = s.selfTest(); err != nil {
		return nil, fmt.Errorf("CUDA 启动自检失败：%w", err)
	}
	fmt.Println("CUDA 启动自检通过：连续两批地址均与 CPU 一致")
	if err = s.setRules(c); err != nil {
		return nil, err
	}
	s.seeds = make([]*big.Int, o.lanes)
	points := make([]byte, o.lanes*64)
	// 每条搜索流使用独立的 256 位安全随机私钥，预留整个 uint64 偏移区间。
	upper := new(big.Int).Sub(crypto.S256().Params().N, new(big.Int).Lsh(big.NewInt(1), 64))
	for i := range s.seeds {
		for {
			key, e := crypto.GenerateKey()
			if e != nil {
				return nil, e
			}
			if key.D.Cmp(upper) >= 0 {
				continue
			}
			s.seeds[i] = key.D
			encodePoint(points[i*64:(i+1)*64], &key.PublicKey)
			break
		}
	}
	if err = d.put(s.points, points); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func (s *cudaSearcher) close() { s.d.close(); runtime.UnlockOSThread() }

func encodeField(dst []byte, n *big.Int) {
	b := n.FillBytes(make([]byte, 32))
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(dst[i*4:], binary.BigEndian.Uint32(b[28-i*4:]))
	}
}

func encodePoint(dst []byte, p *ecdsa.PublicKey) { encodeField(dst, p.X); encodeField(dst[32:], p.Y) }

func (s *cudaSearcher) setRules(c config) error {
	b := make([]byte, 8+32*4+32*40)
	binary.LittleEndian.PutUint32(b, uint32(c.Continuous))
	binary.LittleEndian.PutUint32(b[4:], uint32(len(c.DreamAddressSubstr)))
	for i, part := range c.DreamAddressSubstr {
		binary.LittleEndian.PutUint32(b[8+i*4:], uint32(len(part)))
		copy(b[8+32*4+i*40:], part)
	}
	return s.d.put(s.rules, b)
}

func (s *cudaSearcher) batch(lanes, steps int, all bool) ([]gpuHit, error) {
	if err := s.d.put(s.count, make([]byte, 4)); err != nil {
		return nil, err
	}
	if err := s.d.put(s.fault, make([]byte, 4)); err != nil {
		return nil, err
	}
	var mode uint64
	if all {
		mode = 1
	}
	if err := s.d.launch(s.function, lanes, s.points, s.rules, s.hits, s.count, s.fault, uint64(steps), mode); err != nil {
		return nil, err
	}
	b := make([]byte, 4)
	if err := s.d.get(s.fault, b); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(b) != 0 {
		return nil, errors.New("CUDA 点运算遇到无穷远点，已停止搜索")
	}
	if err := s.d.get(s.count, b); err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n > lanes*steps {
		return nil, errors.New("CUDA 返回的命中数量越界")
	}
	data := make([]byte, n*28)
	if err := s.d.get(s.hits, data); err != nil {
		return nil, err
	}
	hits := make([]gpuHit, n)
	for i := range hits {
		hits[i].lane = binary.LittleEndian.Uint32(data[i*28:])
		hits[i].iteration = binary.LittleEndian.Uint32(data[i*28+4:])
		copy(hits[i].address[:], data[i*28+8:i*28+28])
		if hits[i].lane >= uint32(lanes) || hits[i].iteration >= uint32(steps) {
			return nil, errors.New("CUDA 返回的命中位置越界")
		}
	}
	return hits, nil
}

func (s *cudaSearcher) selfTest() error {
	points := make([]byte, 128*64)
	for i := 0; i < 128; i++ {
		key, err := crypto.ToECDSA(new(big.Int).SetInt64(int64(i + 1)).FillBytes(make([]byte, 32)))
		if err != nil {
			return err
		}
		encodePoint(points[i*64:], &key.PublicKey)
	}
	if err := s.d.put(s.points, points); err != nil {
		return err
	}
	for batch := 0; batch < 2; batch++ {
		hits, err := s.batch(128, 4, true)
		if err != nil {
			return err
		}
		if len(hits) != 512 {
			return errors.New("启动自检结果数量不符")
		}
		seen := make(map[uint64]bool)
		for _, hit := range hits {
			id := uint64(hit.lane)*4 + uint64(hit.iteration)
			if seen[id] {
				return errors.New("启动自检返回重复位置")
			}
			seen[id] = true
			k := int64(hit.lane) + 1 + int64(batch*4) + int64(hit.iteration)
			key, e := crypto.ToECDSA(big.NewInt(k).FillBytes(make([]byte, 32)))
			if e != nil {
				return e
			}
			if crypto.PubkeyToAddress(key.PublicKey) != hit.address {
				return errors.New("启动自检地址不一致")
			}
		}
	}
	return nil
}

func (s *cudaSearcher) run(ctx context.Context, sink func(*ecdsa.PrivateKey, common.Address) error, tried *atomic.Uint64) error {
	for ctx.Err() == nil {
		if s.offset > math.MaxUint64-uint64(s.steps) {
			return errors.New("搜索偏移已用尽，请重启以重新生成随机种子")
		}
		hits, err := s.batch(s.lanes, s.steps, false)
		if err != nil {
			return err
		}
		tried.Add(uint64(s.lanes * s.steps))
		seen := make(map[uint64]bool, len(hits))
		for _, hit := range hits {
			id := uint64(hit.lane)*uint64(s.steps) + uint64(hit.iteration)
			if seen[id] {
				return errors.New("CUDA 返回重复命中，已停止搜索")
			}
			seen[id] = true
			k := new(big.Int).Add(s.seeds[hit.lane], new(big.Int).SetUint64(s.offset+uint64(hit.iteration)))
			key, e := crypto.ToECDSA(k.FillBytes(make([]byte, 32)))
			if e != nil {
				return fmt.Errorf("重建私钥失败：%w", e)
			}
			addr := crypto.PubkeyToAddress(key.PublicKey)
			if addr != hit.address || !s.c.matches(addr) {
				return errors.New("GPU 命中结果未通过 CPU 复核，已停止搜索")
			}
			if err = sink(key, addr); err != nil {
				return err
			}
		}
		s.offset += uint64(s.steps)
	}
	return ctx.Err()
}
