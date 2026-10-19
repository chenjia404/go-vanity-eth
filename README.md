# Go-Vanity-ETH

以太坊靓号地址生成器，支持 CPU 与 CUDA。命中地址保存前均使用 go-ethereum 计算和复核。

## Windows 快速使用

CUDA 后端支持 Windows 64 位与 NVIDIA 显卡。保留以下目录结构：

```text
go-vanity-eth.exe
run-cuda.cmd
config.json
cuda/
  vanity.ptx
```

双击 `run-cuda.cmd` 即可使用显卡搜索，窗口会显示显卡名称、自检结果和每秒搜索数量。双击 EXE 默认使用 CPU；也可以在项目目录的 PowerShell 7 中运行：

```powershell
# 显卡搜索，持续运行，按 Ctrl+C 停止
./go-vanity-eth.exe -backend cuda

# 生成一个符合规则的地址后退出
./go-vanity-eth.exe -backend cuda -limit 1

# 显卡测速 10 秒，不保存地址或私钥
./go-vanity-eth.exe -backend cuda -benchmark -duration 10s

# CPU 搜索；可自行指定工作协程数
./go-vanity-eth.exe -backend cpu -workers 24
```

默认追加写入 `wallet.txt`，每条记录依次为地址、私钥和空行。终端只显示地址，不显示私钥。可用 `-output` 指定输出文件，用 `-config` 指定配置文件。使用相对路径时，以终端当前目录为准；双击启动脚本会自动切换到程序目录。

## 匹配规则

```json
{
  "continuous": 8,
  "dreamAddressSubstr": []
}
```

- `continuous`：地址开头或结尾连续相同字符的数量，范围 1 到 40；0 表示关闭此规则。
- `dreamAddressSubstr`：地址中需要包含的子串，最多 32 个，每个为 1 到 40 个十六进制字符，不包含 `0x`。
- 多个条件使用“或”关系，满足连续字符条件或任意一个子串即命中。至少启用一种规则。
- 匹配 EIP-55 校验和地址，区分字母大小写。例如 `aaaa` 与 `AAAA` 不相同，数字不受影响。GPU 与 CPU 的规则一致。

例如，仅匹配包含 `888888` 的地址：

```json
{
  "continuous": 0,
  "dreamAddressSubstr": ["888888"]
}
```

## 构建

使用 PowerShell 7，默认 UTF-8。需要 Go（版本不低于 `go.mod` 的要求）；编译 CUDA 内核还需要 CUDA Toolkit 与 Visual Studio C++ 桌面开发工具。运行已编译程序仅需 NVIDIA 驱动与配套 PTX，无需安装 Toolkit。

```powershell
# 同时编译 CUDA 内核和可直接运行的 Windows EXE
./scripts/build.ps1

# 仅编译 CUDA 内核
./scripts/build-cuda.ps1

# 仅构建 Go 程序，CUDA 后端也不需要 CGO
$env:CGO_ENABLED = '0'
go build -o go-vanity-eth.exe .

# 源码运行，必须使用整个包
$env:CGO_ENABLED = '0'
go run . -backend cuda
```

默认内核目标为 `compute_86`，适用于本机 RTX 3060 Ti 及兼容的后续显卡。其他显卡可使用 `-Architecture compute_XX` 指定对应架构，具体取决于 Toolkit 支持范围。架构选择参见 [NVIDIA 官方编译器文档](https://docs.nvidia.com/cuda/cuda-compiler-driver-nvcc/index.html#gpu-feature-list)。

若 MSVC 版本超过已安装 Toolkit 支持范围，优先安装二者兼容的工具链。本机 CUDA 12.4 配合 MSVC 14.44 使用以下选项编译 PTX，并已通过实机测试：

```powershell
./scripts/build.ps1 -AllowUnsupportedCompiler
```

此选项仅跳过 nvcc 的宿主编译器版本检查，不代表任意工具链组合均兼容。也可用 `-CompilerDirectory` 指定兼容的 MSVC `cl.exe` 所在目录。

本机默认 CGO 构建的独立 EXE 曾出现 Windows 无法启动的问题；构建脚本固定使用 `CGO_ENABLED=0`，此构建已验证可以直接启动。Linux、macOS 和其他架构可构建 CPU 版本；目前不提供这些平台的 CUDA 驱动适配。

## CUDA 实现与参数

CPU 为每个搜索线程生成独立的安全随机私钥，仅将公钥上传显卡。GPU 通过 secp256k1 点递增、warp 批量求逆、Keccak-256 与 EIP-55 计算进行批量搜索。CPU 根据初始私钥和命中偏移重建私钥，重新验证地址与匹配条件，通过后才保存。

每次启动先执行两批共 1024 个已知地址的 CPU/GPU 对照自检。显卡错误或复核失败会停止搜索，不会自动切换后端或继续保存。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-device` | `0` | 显卡编号 |
| `-lanes` | `4096` | 搜索线程数，128 到 65536，须为 128 的倍数 |
| `-steps` | `8` | 每批每线程尝试次数，1 到 64 |
| `-ptx` | `cuda/vanity.ptx` | 内核文件路径 |
| `-duration` | `0` | 运行时长，例如 `30s`；0 表示不限时 |
| `-limit` | `0` | 保存数量上限；0 表示不限量，测速模式忽略此项 |

停止信号在当前批次完成后处理。显卡同时承担桌面显示时，可以降低 `-lanes` 或 `-steps` 缩短单批执行时间。

2026-10-19 本机测试：RTX 3060 Ti、默认 CUDA 参数、`continuous=8`，10 秒约尝试 4296 万个地址，平均约 **429 万地址/秒**。i9-12900KF 的 CPU 后端使用 24 个工作协程，5 秒约 **39 万地址/秒**。这是当前实现的本机测量，其他硬件、负载和匹配规则的结果会不同；容易命中的规则会增加 CPU 复核及文件写入开销。

## 验证

```powershell
$env:CGO_ENABLED = '0'
go test ./...
go vet ./...

# 需要已编译的 cuda/vanity.ptx 和 NVIDIA 显卡
$env:CUDA_TEST = '1'
go test -count=1 -v ./...
```

实机测试包括：1024 组素域加减乘与 Go 大整数逐项对照、随机私钥地址、点加倍、跨批次状态以及 GPU/CPU 匹配集合一致性。未设置 `CUDA_TEST=1` 时会跳过显卡测试。

## 私钥说明

随机种子使用 go-ethereum 的安全随机私钥生成方法，不使用时间戳或 GPU 伪随机数初始化。每条 GPU 搜索流基于一个随机私钥递增，泄露其中一个私钥可能影响同一搜索流中的其他私钥，因此应妥善保护整个输出文件，勿上传或分享。程序免费提供，不提供安全承诺。
