package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type config struct {
	Continuous         int      `json:"continuous"`
	DreamAddressSubstr []string `json:"dreamAddressSubstr"`
}

func (c config) validate() error {
	if c.Continuous < 0 || c.Continuous > 40 {
		return errors.New("continuous 必须在 0 到 40 之间")
	}
	if len(c.DreamAddressSubstr) > 32 {
		return errors.New("dreamAddressSubstr 最多支持 32 个字符串")
	}
	if c.Continuous == 0 && len(c.DreamAddressSubstr) == 0 {
		return errors.New("至少启用一种地址匹配规则")
	}
	for _, s := range c.DreamAddressSubstr {
		if len(s) == 0 || len(s) > 40 || strings.Trim(s, "0123456789abcdefABCDEF") != "" {
			return errors.New("地址子串须为 1 到 40 个十六进制字符，不含 0x")
		}
	}
	return nil
}

func (c config) matches(address common.Address) bool {
	s := address.Hex()[2:]
	if n := c.Continuous; n > 0 {
		if strings.Count(s[:n], s[:1]) == n || strings.Count(s[40-n:], s[40-n:41-n]) == n {
			return true
		}
	}
	for _, part := range c.DreamAddressSubstr {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

type options struct {
	backend, configPath, output, ptx     string
	workers, device, lanes, steps, limit int
	duration                             time.Duration
	benchmark                            bool
}

type searcher interface {
	run(context.Context, func(*ecdsa.PrivateKey, common.Address) error, *atomic.Uint64) error
	close()
}

var errLimit = errors.New("已达到生成数量")

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func execute() error {
	var o options
	flag.StringVar(&o.backend, "backend", "cpu", "计算后端：cpu 或 cuda")
	flag.StringVar(&o.configPath, "config", "config.json", "匹配规则文件")
	flag.StringVar(&o.output, "output", "wallet.txt", "地址与私钥输出文件")
	flag.StringVar(&o.ptx, "ptx", "cuda/vanity.ptx", "CUDA 内核文件")
	flag.IntVar(&o.workers, "workers", maxInt(1, (runtime.NumCPU()-2)/4), "CPU 工作协程数")
	flag.IntVar(&o.device, "device", 0, "CUDA 显卡编号")
	flag.IntVar(&o.lanes, "lanes", 4096, "CUDA 搜索线程数，必须是 128 的倍数")
	flag.IntVar(&o.steps, "steps", 8, "CUDA 每批每线程尝试次数，范围 1 到 64")
	flag.IntVar(&o.limit, "limit", 0, "生成指定数量后退出，0 表示持续搜索")
	flag.DurationVar(&o.duration, "duration", 0, "运行时长，例如 30s；0 表示不限时")
	flag.BoolVar(&o.benchmark, "benchmark", false, "测速模式，不保存地址或私钥，默认运行 10 秒")
	flag.Parse()
	if o.workers < 1 || o.limit < 0 || o.duration < 0 || o.device < 0 {
		return errors.New("工作协程数必须大于 0；数量、时长和显卡编号不能为负数")
	}
	if o.benchmark && o.duration == 0 {
		o.duration = 10 * time.Second
	}
	data, err := os.ReadFile(o.configPath)
	if err != nil {
		return fmt.Errorf("读取配置失败：%w", err)
	}
	var c config
	if err = json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("配置格式错误：%w", err)
	}
	if err = c.validate(); err != nil {
		return err
	}
	var engine searcher
	switch o.backend {
	case "cpu":
		engine = &cpuSearcher{c: c, workers: o.workers}
	case "cuda":
		engine, err = newCUDASearcher(c, o)
		if err != nil {
			return err
		}
	default:
		return errors.New("backend 只能是 cpu 或 cuda")
	}
	defer engine.close()
	var file *os.File
	if !o.benchmark {
		file, err = os.OpenFile(o.output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("打开输出文件失败：%w", err)
		}
		defer file.Close()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if o.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.duration)
		defer cancel()
	}
	var tried, found atomic.Uint64
	var mu sync.Mutex
	sink := func(key *ecdsa.PrivateKey, addr common.Address) error {
		mu.Lock()
		defer mu.Unlock()
		if o.limit > 0 && found.Load() >= uint64(o.limit) && !o.benchmark {
			return errLimit
		}
		if file != nil {
			entry := addr.Hex() + "\n" + hex.EncodeToString(crypto.FromECDSA(key)) + "\n\n"
			n, e := io.WriteString(file, entry)
			if e != nil {
				return fmt.Errorf("保存钱包失败：%w", e)
			}
			if n != len(entry) {
				return io.ErrShortWrite
			}
			if e = file.Sync(); e != nil {
				return fmt.Errorf("同步钱包文件失败：%w", e)
			}
			fmt.Println("已保存地址：", addr.Hex())
		}
		found.Add(1)
		if o.limit > 0 && found.Load() >= uint64(o.limit) && !o.benchmark {
			return errLimit
		}
		return nil
	}
	fmt.Printf("开始搜索，后端：%s，测速：%t\n", o.backend, o.benchmark)
	start := time.Now()
	done := make(chan struct{})
	var progress sync.WaitGroup
	progress.Add(1)
	go func() {
		defer progress.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("已尝试 %d，平均 %.0f 地址/秒，命中 %d\n", tried.Load(), float64(tried.Load())/time.Since(start).Seconds(), found.Load())
			}
		}
	}()
	err = engine.run(ctx, sink, &tried)
	close(done)
	progress.Wait()
	fmt.Printf("搜索结束：尝试 %d，命中 %d，用时 %.3f 秒，平均 %.0f 地址/秒\n", tried.Load(), found.Load(), time.Since(start).Seconds(), float64(tried.Load())/time.Since(start).Seconds())
	if errors.Is(err, errLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

type cpuSearcher struct {
	c       config
	workers int
}

func (*cpuSearcher) close() {}

func (s *cpuSearcher) run(ctx context.Context, sink func(*ecdsa.PrivateKey, common.Address) error, tried *atomic.Uint64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errorsCh := make(chan error, 1)
	fail := func(err error) {
		select {
		case errorsCh <- err:
		default:
		}
		cancel()
	}
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				key, err := crypto.GenerateKey()
				if err != nil {
					fail(fmt.Errorf("生成安全随机私钥失败：%w", err))
					return
				}
				addr := crypto.PubkeyToAddress(key.PublicKey)
				tried.Add(1)
				if s.c.matches(addr) {
					if err = sink(key, addr); err != nil {
						fail(err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errorsCh:
		return err
	default:
		return ctx.Err()
	}
}
